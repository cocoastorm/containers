# qBittorrent NAT-PMP sync: architecture

See [README.md](README.md) for configuration and build commands. This document
explains the design: what the helper owns, its invariants, and why.

## Problem

qBittorrent reaches the internet through a Proton VPN tunnel terminated on
OPNsense rather than in a Gluetun sidecar. Something must still obtain and renew
Proton's incoming-port lease and tell qBittorrent which public port to
advertise. Gluetun's port-sync sidecar cannot do this: it reads Gluetun and
changes qBittorrent's *listener*.

The helper owns the Proton mapping and keeps qBittorrent's **advertised** port
in sync, while the local listener and router forward stay fixed:

```text
Peer -> Proton public IP:assigned port
     -> OPNsense tunnel IP:fixed port
     -> qBittorrent VLAN30 IP:fixed port

Helper -> Proton NAT-PMP: obtain and renew TCP/UDP mappings
Helper -> qBittorrent Web API: set announce_port to assigned public port
```

The assigned public port may change. The helper needs no OPNsense credentials
and makes no dynamic firewall edits.

## Scope

- One gateway, one fixed internal port, one qBittorrent instance, IPv4,
  TCP and UDP. No provider framework, UI, router agent, or multi-client
  allocation.
- Runs as a sidecar in qBittorrent's pod. Only one instance may own the mapping
  for a given tunnel. A Recreate deployment helps, but it cannot fence off a
  stranded old pod.
- It does not use the OPNsense or Kubernetes APIs, host networking, privileged
  mode, `NET_ADMIN`, qBittorrent config-file edits, or a persistent volume.
- MAM, split DNS, routing, and VPN configuration belong to other components.

## Code layout

```text
cmd/qbittorrent-natpmp-sync/  # config parsing, logging, status server, wiring
internal/lease/               # NAT-PMP wire adapter (one bounded exchange)
internal/qbittorrent/         # Web API client and preference validation
internal/sync/                # mapping + qBittorrent loops, shared state, /status
```

## Runtime design

One process runs two independent loops that share the latest validated mapping.
A slow or failing qBittorrent request never blocks lease renewal.

### Mapping loop (`internal/lease`, `internal/sync`)

- Sends requests to the configured `NATPMP_GATEWAY`, not the VLAN's default
  router. It can bind to `SOURCE_IP` so the router's narrow UDP/5351 rule matches.
- Requests the configured nonzero internal port. At first it lets Proton choose
  the public port (requested external port 0). After that it asks for the
  already-assigned port for the other protocol and on renewal.
- Validates each reply: opcode, result code, expected internal port, a nonzero
  public port, and a positive lifetime. The requested public port is never
  assumed to have been granted.
- Publishes a usable mapping only while **both** TCP and UDP leases are
  unexpired and share the same public port. A partial or mismatched pair is
  reported as degraded, never as healthy.
- Tracks each protocol's expiry separately. Renewal is scheduled at half the
  *granted* lifetime, not at a fixed interval. A failed attempt retries after
  2 seconds.
- A gateway epoch rollback, which means the gateway restarted, invalidates both
  leases and starts reacquisition.
- Each attempt uses a fresh connected UDP socket, so only the gateway can reply
  and a stale reply is never mistaken for a new one. Attempts wait 250 ms,
  500 ms, 1 s, then 2 s, all within a 4-second deadline. The adapter is self-contained:
  [`jackpal/go-nat-pmp`](https://github.com/jackpal/go-nat-pmp/blob/master/natpmp.go)
  supports an explicit gateway but lacks the cancellation and reply validation
  needed here.
- On shutdown, retries stop and leases are allowed to expire. The helper never
  sends deletions, because another owner may already be taking over.
- When the validated public port changes, the qBittorrent loop is notified.

### qBittorrent loop (`internal/qbittorrent`, `internal/sync`)

- Calls the Web API at `QBITTORRENT_URL` (default `http://127.0.0.1:8080`)
  without credentials, relying on qBittorrent's localhost authentication bypass.
  It has no auth mode. URL-embedded credentials, queries, and fragments are
  rejected, and redirects and proxies are disabled. Connect and request
  timeouts are bounded.
- Logs the qBittorrent version once at startup, for diagnosis only. There is no
  compatibility matrix.
- Reconciles at startup, whenever the mapping changes, and every 30 seconds to
  catch drift. After an error it retries in 3 seconds.
- Each reconciliation:
  1. reads preferences and validates the fixed-listener contract:
     `listen_port == INTERNAL_PORT`, `random_port == false`, `upnp == false`;
  2. if `announce_port` differs from the mapping, writes it, then reads the
     preferences back and checks again;
  3. reports the port as synchronized only if the mapping did not change while
     the HTTP calls were in flight.
- Setting drift is reported with expected and observed values, but those
  settings are never modified. Lease renewal continues regardless.
- The only write is `announce_port`. This boundary is enforced in the client:
  `SetAnnouncePort` sends a single-field JSON payload and never serializes a
  `Preferences` object. The API session may have broader permissions, so this
  is a guarantee made by the application, not something the server enforces per
  field. `announce_ip` is left empty and untouched.
- `setPreferences` succeeds on HTTP 200 or 204. qBittorrent 5.2+ returns 204
  for successful empty Web API responses. Read endpoints accept only 200, so an
  empty success is never parsed as data.
- The helper does not trigger per-torrent reannounces. Add a bounded
  reannounce path only if live tracker/DHT observation shows that changes to the
  advertised port propagate too slowly.

## Failure behavior and health

- While Proton or qBittorrent is unavailable, the helper keeps renewing and
  retrying. It never pauses torrents or changes transfers. An expired mapping
  becomes invalid at once in helper state, and the last configured
  `announce_port` is left in place rather than replaced with an unverified
  fallback.
- `GET http://127.0.0.1:8099/status` serves cached state without making network
  requests. It returns HTTP 200 even when the helper is degraded. The response
  includes `healthy`/`degraded` with a reason, the internal and public ports,
  each lease's time to expiry, and the last success, last error, and failure
  count for each loop. The endpoint is localhost-only: do not expose it via an
  Ingress or wire it into pod readiness. Helper degradation must not take the
  Web UI out of its Service.
- No liveness probe is needed. The process exits only on an unrecoverable
  internal failure, and Kubernetes then restarts it.
- `/status` reports control-plane state only. It does **not** prove incoming
  peer delivery or leak protection, and it is not a VPN kill switch.

### Logging

- Structured JSON goes to stdout with UTC timestamps and stable event names
  such as `mapping_available`, `announce_port_write`, `*_degraded`,
  `*_recovered`, and `gateway_epoch_reset`.
- A failure is logged when it first occurs or when its reason changes. Repeats
  only increment a counter. A `status_summary` line is written every 5 minutes.
  Individual mapping attempts are logged at debug level.
- Error categories are kept distinct: transport timeout, NAT-PMP rejection,
  reply mismatch, partial or mismatched lease, API rejection (with HTTP status),
  settings drift, and readback mismatch.
- Cookies, authorization headers, and response bodies are never logged.

## Deployment prerequisites

Home-ops provides these. The helper can observe some of them but cannot create
or certify them:

1. A Proton tunnel with port forwarding enabled and no competing mapping owner
   on that tunnel.
2. qBittorrent on its reserved VLAN30 address (`192.168.30.50`) with a route to
   the Proton gateway (`10.2.0.1`) through OPNsense.
3. A narrow UDP/5351 allowance from that address to the gateway. Without it,
   NAT-PMP queries time out, and DNS access alone is not enough.
4. A fixed TCP/UDP forward on the tunnel to qBittorrent, plus the matching pass
   and reply-routing rules. Changing `INTERNAL_PORT` requires coordinated edits
   to both qBittorrent and this rule.
5. qBittorrent configured with that fixed listener, random port and native
   UPnP/NAT-PMP disabled, and Web API access from the sidecar.
6. Existing VLAN routing, DNS, and the VPN-only egress policy.

## Acceptance before relying on it

A passing build or API readback is not end-to-end proof. During a cutover:

- back up the persisted announce, listen, interface, and mapping settings;
- on the real tunnel, prove the fixed internal mapping, incoming TCP/UDP
  delivery with correct replies, and at least one renewal;
- confirm that trackers (and DHT, if applicable) see the assigned public port
  while the listener stays fixed.

If fixed-port delivery or runtime announcement updates fail, stop and revisit
the design. Do not broaden NAT rules, grant router credentials, or restart
qBittorrent to hide the problem. To roll back: stop the helper, disable the
dedicated forward, restore the previous workload and settings, and resume only
the torrents that were previously running.

## Evidence

- [Gluetun's Proton implementation](https://github.com/qdm12/gluetun/blob/e2e4a0658aada9751603bacd2036131319d4a82b/internal/provider/protonvpn/portforward.go)
  requests fixed internal ports, checks that TCP and UDP got the same public
  port, and renews both.
- A 2026-09-17 test pod on VLAN30 got a fixed-internal-port mapping from this
  Proton gateway for both TCP and UDP with 60-second leases, and renewed it
  with the same port pair. Incoming delivery was not tested then.
- [Proton's manual](https://protonvpn.com/support/port-forwarding-manual-setup)
  documents short NAT-PMP leases. Its internal-port-zero mode is a different
  contract and must not be substituted.
- [qBittorrent session code](https://github.com/qbittorrent/qBittorrent/blob/release-5.1.4/src/base/bittorrent/sessionimpl.cpp)
  and [libtorrent 2.0.11](https://github.com/arvidn/libtorrent/blob/v2.0.11/src/session_impl.cpp)
  apply `announce_port` at runtime. libtorrent >= 2.0.11 is required, and the 1.2
  builds lack this path.
  [Libtorrent's settings](https://www.libtorrent.org/reference-Settings.html#announce_port)
  separate the advertised port from the listener.
- [daball's helper](https://github.com/daball/qbittorrent-natpmp-sync/blob/4b4a169be3c67e1b510752185ce6fdf2ba399c9f/main.go)
  uses the same overall architecture. The behaviors deliberately avoided here
  are coupling renewal to API success, missing HTTP timeouts, Basic Auth, and
  only warning on mismatched ports.
