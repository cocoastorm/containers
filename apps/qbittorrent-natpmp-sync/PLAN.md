# qBittorrent NAT-PMP sync: application plan

Date: 2026-09-16. Updated: 2026-09-17. Status: planning complete;
Q1–Q4 and the v1 simplifications accepted. A temporary VLAN30 pod verified
the Proton gateway's fixed-port NAT-PMP behavior. Implementation, publication,
and the home-ops cutover are separate work. The user accepted the product
decisions below; technical defaults are proposed implementation choices, subject
to the documented compatibility and live-validation gates.

## Problem and intended result

Moving qBittorrent's VPN connection from Gluetun to OPNsense removes the
component that obtains and renews Proton's incoming-port lease. The existing
Gluetun port-sync sidecar cannot fill that gap: it reads Gluetun and changes
qBittorrent's listener.

Build a small helper that owns the Proton mapping and keeps qBittorrent's
advertised public port synchronized. Keep the local listener and router
forward fixed:

```text
Peer -> Proton public IP:assigned port
     -> OPNsense tunnel IP:fixed port
     -> qBittorrent VLAN30 IP:fixed port

Helper -> Proton NAT-PMP: obtain and renew TCP/UDP mappings
Helper -> qBittorrent Web API: set announce_port to assigned public port
```

The assigned public port can change. The helper does not need OPNsense
administration credentials or dynamic firewall edits.

## Evidence and limits

Source context is the Codex task [Explain OPNsense VLAN30 setup](thread://01a0ac55-d530-7222-9bec-a4f5f974ba6e?hostId=local),
particularly its final research correction, rather than earlier statements
that dynamic router updates were mandatory.

The local research is
`/home/khoa/infra/home-ops/docs/QBITTORRENT-FIXED-PORT-RESEARCH.md`;
the migration handoff is
`/home/khoa/infra/home-ops/plans/QBITTORRENT-VLAN30-CUTOVER-PLAN.md`.
These are local context, not dependencies available to future clones. The
essential findings are recorded here with primary-source links:

- [Gluetun's inspected Proton implementation](https://github.com/qdm12/gluetun/blob/e2e4a0658aada9751603bacd2036131319d4a82b/internal/provider/protonvpn/portforward.go)
  requests fixed internal ports for additional mappings, checks the returned
  internal port and TCP/UDP public-port agreement, and renews both mappings.
  This supports the proposed mechanism.
- A 2026-09-17 pod test on VLAN30 verified it against this Proton tunnel:
  after adding a narrow UDP 5351 router allowance for test address
  `192.168.30.100`, the gateway returned public address `212.104.215.3` and
  granted internal port `55873` to public port `52837` for both TCP and UDP,
  with 60-second leases. Both renewed with the same port pair. The numbers
  are test values, not production assignments. Incoming delivery and
  qBittorrent behavior remain untested.
- [Proton's manual](https://protonvpn.com/support/port-forwarding-manual-setup)
  documents short NAT-PMP leases (60 seconds in its example, renewed every
  45 seconds). Its special internal-zero request is a different mode and must
  not be silently substituted for the fixed-port contract.
- [qBittorrent 5.1.4 source](https://github.com/qbittorrent/qBittorrent/blob/release-5.1.4/src/base/bittorrent/sessionimpl.cpp)
  and [libtorrent 2.0.11 source](https://github.com/arvidn/libtorrent/blob/v2.0.11/src/session_impl.cpp)
  support updating the advertised port in the session. Prior read-only checks
  found qBittorrent's `announce_port` preference and libtorrent 2.0.11.0.
  The UI says restart required: verify actual announcements on the deployed
  version; a successful API readback alone is insufficient.
- [Libtorrent's setting documentation](https://www.libtorrent.org/reference-Settings.html#announce_port)
  distinguishes the advertised port from the listener.
- [daball's helper](https://github.com/daball/qbittorrent-natpmp-sync/blob/4b4a169be3c67e1b510752185ce6fdf2ba399c9f/main.go)
  demonstrates the architecture, but the inspected version couples renewal
  to API success, lacks HTTP timeouts, uses unsuitable Basic Auth, and only
  warns on mismatched public ports. Do not copy those behaviors.
- [Seaport](https://github.com/ls0t/seaport/tree/23dd6b6f3ed69be8deb425fa9268b9ea6e3f7bbe)
  is a plausible extension alternative, but its inspected qBittorrent action
  changes `listen_port`, and its scheduling needs review for this use case.

## Recommended scope

- Working name and directory: `apps/qbittorrent-natpmp-sync/`.
- Small purpose-built Go program, with its source in this directory. Use a
  maintained NAT-PMP library after checking explicit-gateway support,
  cancellation, retries, response validation, and license compatibility.
- Run as a normal sidecar in qBittorrent's pod. Default to localhost for its
  Web API, while allowing an operator-configured HTTP(S) URL, and use the
  shared VLAN30 network path for Proton. One instance owns the mapping
  on the OPNsense tunnel; Recreate deployment and operational fencing prevent
  overlap. Recreate alone does not fence a stranded old pod.
- One gateway, one fixed internal port, one qBittorrent instance, IPv4 and
  both TCP/UDP for v1. No provider framework, UI, router agent, or multi-client
  allocation service.
- Leave MAM, split DNS, route initialization, and VPN configuration in their
  existing components. They belong to the cutover, not this helper.
- No OPNsense API, Kubernetes API, host networking, privileged mode,
  NET_ADMIN, shared qBittorrent config-file editing, or persistent volume.

## External prerequisites

Home-ops supplies these; the helper verifies its own observable conditions
but cannot create or certify the router configuration:

1. A port-forwarding-enabled Proton tunnel and no competing mapping owner
   for that tunnel. Gluetun's old tunnel may be distinct; identify owners by
   tunnel rather than assuming every Proton client conflicts.
2. The planned qBittorrent address `192.168.30.50`, already reserved in the
   prior task, with routing to Proton gateway `10.2.0.1` through VLAN30 and
   OPNsense. Refresh inventory before deployment; this plan allocates no IP.
3. A narrow UDP 5351 allowance from that address to the Proton gateway,
   appropriate source NAT into the tunnel, and a working return path.
   The temporary pod's NAT-PMP query timed out before its UDP 5351 allowance
   and succeeded afterward; DNS access alone did not suffice. OPNsense now
   has an enabled, logged rule for only `192.168.30.50/32` to
   `10.2.0.1/32:5351` via `PROTON_VLAN30_GW`. Recheck it at deployment.
4. A narrow fixed TCP/UDP forward on WG_PROTON to qBittorrent, with the
   associated pass and reply-routing rules. Choose and check the fixed port
   during integration; `6881` in the research was only illustrative.
5. qBittorrent configured with that fixed listener, random-port and native
   UPnP/NAT-PMP disabled, compatible announce-port behavior, and working
   Web API access from the sidecar. The deployed
   `gluetun-qb-port-sync` passes `QBITTORRENT_HOST=localhost` and the Web UI
   port, with no qBittorrent credentials; verify the same access works after
   the cutover rather than assuming the persisted setting.
6. Existing VLAN routing, DNS, and VPN-only egress policy. Helper readiness
   is not a VPN kill switch or proof of leak prevention.

## Runtime design

Keep two independent loops in one process, sharing the latest validated
mapping. Slow or failed qBittorrent HTTP requests must never block lease
renewal.

### Mapping worker

- Send requests to the explicitly configured Proton gateway, not the VLAN's
  default router. Request the configured nonzero internal port and initially
  let Proton choose the public port (requested external port zero).
- Acquire both protocols, using the assigned public port as the requested
  counterpart where appropriate. Validate actual replies rather than assuming
  the requested public port was granted.
- Require matching nonzero TCP/UDP external ports, the expected internal
  port, and positive lifetimes. Publish a usable mapping only while both
  validated leases are unexpired. Reject a partial or mismatched pair.
- Track each protocol's granted lifetime and expiry separately using monotonic
  time. Renew well before the earlier deadline; initially target half the
  granted lifetime, with bounded retries that do not run past expiry. Do not
  hard-code a five-minute renewal loop or assume every lease is 60 seconds.
- On partial renewal, preserve accurate per-protocol state and retry promptly.
  Do not label an old pair healthy if one protocol has moved or expired.
- Use bounded network operations. Validate response source and request fields;
  account for gateway epoch reset when supported by the chosen library.
- Reacquire after restart or lease loss. Notify the qBittorrent loop when the
  validated public port changes. Before reporting synchronization, compare
  the completed API operation with the latest validated mapping; retry if it
  became stale. No general asynchronous state framework is needed.
- Stop retries on shutdown. Prefer letting short leases expire over sending
  broad mapping deletions; reassess exact-pair deletion only if ownership and
  gateway semantics can be proven safe during handover.

### qBittorrent worker

- Reuse `gluetun-qb-port-sync`'s qBittorrent access by default: call the Web
  API on `localhost:8080` without qBittorrent credentials, relying on the
  localhost authentication bypass used by the current deployment. An
  operator-configured HTTP(S) URL may use another host, standard scheme ports,
  and a base path. The helper still has no authentication mode; URL-embedded
  credentials, redirects, proxies, queries, and fragments remain unsupported.
  The old sidecar's Gluetun API key authenticates to Gluetun only and is
  unnecessary here. If the chosen API URL rejects requests, resolve that
  prerequisite before cutover rather than silently adding authentication.
- Bound connect and request timeouts. Never log cookies, authorization headers,
  or response bodies that might contain secrets.
- Read the required preferences and validate the fixed-listener contract.
  Record the qBittorrent version/build for diagnosis, but do not build a version
  compatibility matrix. Feature presence alone does not prove runtime support.
- Reconcile `announce_port` at startup, on mapping change, and periodically
  (proposed drift check: 30 seconds). Read back after writes. Repair drift
  even when Proton's port has not changed.
- Accepted mutation boundary: v1 writes only `announce_port`. It can read
  `listen_port`, `random_port`, and `upnp` to validate the setup and explain
  drift, but must not change them. Leave `announce_ip` and all other preferences
  untouched. Broader settings control is a possible future extension, not a
  hidden option enabled in v1.
- Enforce this boundary in the client interface: expose a narrow advertised-port
  update method and send only that field, never the full preferences object.
  The API session may have broader account permissions; the write restriction
  is an application guarantee, not a claim of field-scoped server authorization.
- Read the listener and native mapping settings at startup and periodically.
  Expect `listen_port=INTERNAL_PORT`, `random_port=false`, and `upnp=false`.
  Report any mismatch as degraded with expected and observed values; keep
  renewing existing helper leases independently. Do not claim synchronization
  or publish a newly acquired announce_port while these prerequisites fail.
  Retry validation so manual correction recovers without restarting the helper.
- Manual changes to announce_port are repaired on the next successful
  reconciliation. Changes to the other preferences remain in place and produce
  an actionable diagnostic. Log repairs without repeatedly writing already-correct
  settings; use a bounded retry delay for rejected writes.
- Deployment configures the fixed listener and disables native mapping/random
  port selection. Deliberate INTERNAL_PORT changes require coordinated edits to
  both qBittorrent and the fixed OPNsense rule. The helper cannot discover or
  reconcile that external router configuration.
- Back up the previous announce_port before activation; the broader cutover
  also backs up any listener/interface/mapping settings it changes. Do not
  automatically restore preferences on helper exit. Validate effective listener
  and runtime announcement behavior beyond preference readback.
- Do not add per-torrent reannounce behavior in v1. During integration, observe
  actual tracker and DHT announcements after an advertised-port change. Add a
  bounded reannounce path only if that evidence shows it is needed.
- After a write and readback, compare the synced port with the latest valid
  mapping. If it changed while HTTP was in flight, reconcile again before
  reporting synchronization.

## Failure behavior and health

Accepted behavior is to keep renewing and retrying while qBittorrent is
unavailable, and to leave running transfers alone when inbound mapping fails.
Existing outbound connections may still work. Expired mappings become invalid
immediately in helper state; retain the last configured announce value while
degraded rather than advertising an unverified fallback number.

Expose one localhost-only `/status` endpoint. Report `healthy` or `degraded`,
the current internal/public ports, each protocol's time to expiry, and separate
last success time and last error for the mapping and qBittorrent loops. Include
a concise reason when degraded. This reports control-plane state, not incoming
peer reachability. Serve the cached state without initiating network requests.
The endpoint remains responsive and returns HTTP 200 during Proton or
qBittorrent outages. For v1, let Kubernetes restart the container if the
process exits on an unrecoverable internal failure; no sidecar liveness probe
is needed. Do not add a separate readiness endpoint or wire mapping/sync
state into Kubernetes readiness. Keep the application's own readiness check
and Web UI Service access. Verify during integration that simulated helper
degradation leaves Web UI Service endpoints available without restarting the
pod. No public Ingress for helper status. Add metrics only if existing
monitoring will consume them.

### Logging and diagnosis

- Write structured JSON logs to stdout with UTC timestamps, level, a stable
  event name, and fields relevant to the event. Use normal-level logs for
  startup configuration (excluding secrets), mapping acquisition or public-port
  changes, announce-port writes, degradation, and recovery. Use debug logs for
  individual request attempts and routine successful renewals. Emit a concise
  periodic normal-level summary so healthy operation remains visible without
  logging every short renewal at that level.
- Include gateway, protocol, expected and returned internal/public ports,
  granted lifetime or remaining time, operation duration, and safe error
  category where relevant. For qBittorrent failures include the operation and
  HTTP status; for setting drift include expected and observed values. Distinguish
  transport timeout, NAT-PMP rejection, reply mismatch, lease expiry, API
  rejection, and readback mismatch. Log recovery when a degraded condition clears.
- Avoid flooding logs during a persistent outage: log the first failure or a
  changed reason immediately, then summarize repeated failures periodically
  with an attempt count and last-seen time. Never log credentials, cookies,
  authorization headers, torrent data, or raw secret-bearing response bodies.
- Keep the last success and error for each loop in `/status` so an operator can
  tell a mapping failure from an API failure without searching multiple logs.

## Configuration and packaging

Proposed environment contract, finalized during implementation:

| Setting | Purpose |
| --- | --- |
| `NATPMP_GATEWAY` | Required explicit IPv4 gateway; deployment supplies `10.2.0.1` |
| `INTERNAL_PORT` | Required fixed tunnel/listener port; no implicit assignment |
| `QBITTORRENT_URL` | Default `http://127.0.0.1:8080`; HTTP(S) URL, optionally with a base path; standard ports apply when omitted |
| `LOG_LEVEL` | JSON logging verbosity; default `info` |

Reject invalid settings before starting. Request a 60-second lease initially,
but schedule renewal from the granted lifetime. Check qBittorrent drift every
30 seconds. Keep these intervals and conservative timeout/retry policies as
built-in constants unless live behavior shows a need to tune them.

Planned files:

```text
PLAN.md
README.md
go.mod / go.sum
cmd/qbittorrent-natpmp-sync/main.go
internal/lease/          # protocol adapter and lease state
internal/qbittorrent/    # Web API client
internal/sync/           # reconciliation, status and scheduling
Dockerfile
Dockerfile.dockerignore
docker-bake.hcl
```

Use a multi-stage image build, pinned toolchain/base images, nonroot UID/GID
compatible with home-ops (2000), read-only runtime filesystem, and dropped
capabilities.
Follow existing `image`, `image-local`, and `image-all` bake targets for
linux/amd64 and linux/arm64. Set SOURCE to this repository and use an explicit
app semantic version (initial proposal `0.1.0`), publishing eventually as
`ghcr.io/cocoastorm/qbittorrent-natpmp-sync` and pinning its digest in home-ops.

Provide a Dockerfile-specific ignore file that includes the Go build inputs.
The shared `include/.dockerignore` excludes Go sources. A plain app-level
`.dockerignore` is insufficient for the current local recipe, which copies
the shared file first and then copies app files with `--ignore-existing`.

Repository caveat: pushes to main under `apps/**` enter the release matrix,
including a docs-only new directory. Keep this plan local for now. Before a
plan-only merge, filter discovery to buildable apps or otherwise explicitly
exclude this directory; do not add a placeholder image solely to satisfy CI.
Check the local Taskfile before using its build recipe: its current script
contains an unmatched `esac`. Any necessary build fixes should be minimal and
separate from unrelated repository cleanup.

## Delivery and acceptance

1. Q1–Q4 are settled. Confirm this plan as the basis when implementation is requested.
2. Implement lease and API adapters plus independent reconciliation. Test
   protocol errors, internal-port mismatch, unequal public ports, partial
   renewal, short grants, expiry, gateway restart, and shutdown with fake
   clocks and local fake servers.
3. Test localhost API rejection, timeout, settings drift, missing required
   preferences, and port changes during slow writes. Assert every
   preferences mutation contains only announce_port. Test no-op reconciliation,
   announce-port repair, diagnostics without writes for other settings' drift,
   recovery after manual correction, and rejected writes. Explicitly prove
   blocked qBittorrent HTTP cannot prevent lease renewal. Verify diagnostic
   fields, secret redaction, and degraded `/status` behavior. Run Go tests with
   the race detector and build both target architectures.
4. Add packaging and build verification before publishing a version. A bare
   successful image build is not a protocol integration test.
5. Prepare a separate coordinated home-ops cutover using the already agreed
   single-instance approach. No new canary or mandatory soak/outage matrix.
   Preserve storage, integrations, MAM, and the existing rollback process.
6. During that authorized cutover, prove the fixed internal mapping on the
   actual Proton tunnel, actual incoming TCP/UDP delivery and correct replies,
   at least one renewal, and a tracker announcement using the assigned public
   number while the listener stays fixed. Verify localhost API access, runtime
   updates, and DHT behavior as applicable. If a changed advertised port does
   not reach trackers promptly enough, evaluate a bounded reannounce addition.
   Do not describe API readback as end-to-end proof.
7. If fixed-port delivery or runtime announcement updates fail, stop and revise
   the architecture. Do not automatically broaden NAT rules, grant router
   credentials, or restart qBittorrent to conceal an unmet assumption.

Record the previous persisted announce/listen/interface settings before
cutover. Rollback stops the helper, disables the dedicated router forward,
restores the prior Gluetun workload and persisted settings, verifies its port
sync and integrations, then resumes only previously running torrents. Keep
the `.50` reservation. The helper must not reset preferences on shutdown while
another owner might already be taking over.

## Accepted design decisions

Already established by the request/research: source in this containers repo;
plan only now; investigate fixed-inner forwarding first; no dynamic router
management assumed; use the existing coordinated cutover constraints.

Round 1 decisions:

1. Accepted: purpose-built small Go sidecar, Proton plus qBittorrent only.
2. Accepted: leave transfers alone when incoming mapping fails, report degraded,
   and keep retrying. No automatic torrent pause/resume policy.
3. Accepted: read other settings as needed, but control only announce_port in
   v1. Validate the listener, random-port selection, and native mapping settings;
   report drift without modifying them. Broader control is deferred.

Round 2 decision:

4. Accepted: keep the Web UI available during helper degradation and expose
   helper health separately. Restart only for internal process failure, not
   Proton or qBittorrent API availability; helper mapping health does not gate
   pod readiness.

V1 simplifications accepted on 2026-09-17:

5. Defer per-torrent reannounce until live evidence requires it.
6. Use one localhost status endpoint; keep degradation out of pod readiness.
7. Default to `gluetun-qb-port-sync`'s localhost qBittorrent Web API access,
   with no qBittorrent credentials. Verify it during cutover. The later
   implementation amendment permits an operator-configured HTTP(S) URL on
   another host without adding an authentication mode.
8. Keep lease and drift intervals as built-in constants, and use a simple
   latest-mapping comparison to reject stale API results.
9. Make structured, actionable, secret-safe logging part of v1.

No product-decision questions remain open. Use the research-backed defaults
above for routine implementation choices. Live behavior remains unproven until
acceptance checks pass. Implementation is outside this planning request; confirm
the plan as the implementation basis when that work is requested.
