# qBittorrent NAT-PMP sync

Small, single-instance sidecar for a fixed qBittorrent listener behind a Proton
NAT-PMP gateway. It renews both TCP and UDP mappings to the same fixed internal
port, then writes **only** qBittorrent's advertised `announce_port` through its
Web API. It never changes the listener, router rules, torrents, or
other qBittorrent settings. The mapping and Web API loops run independently.

## Configuration

| Variable | Value |
| --- | --- |
| `NATPMP_GATEWAY` | Required literal IPv4 gateway, e.g. `10.2.0.1` |
| `INTERNAL_PORT` | Required nonzero fixed tunnel/listener port |
| `QBITTORRENT_URL` | HTTP(S) URL, optionally with a base path; default `http://127.0.0.1:8080` |
| `LOG_LEVEL` | `debug`, `info` (default), `warn`, or `error` |

The Web API must accept requests without credentials; the helper does not
implement qBittorrent login. The default URL uses the existing localhost
authentication bypass, but another host can be configured. Standard HTTP(S)
ports apply when the URL omits a port. URL-embedded credentials, queries, and
fragments are rejected; redirects and proxies are disabled. Use HTTPS with a
trusted certificate when the API connection crosses an untrusted network.
The sidecar requires UDP/5351 to the Proton gateway and API access to the
configured URL. The router's fixed TCP/UDP forward and its reply-routing rules,
qBittorrent's fixed listener, `random_port=false`, and `upnp=false` are
prerequisites. It reports drift in those settings but does not change them.
Run only one mapping owner per tunnel; a Recreate deployment alone cannot fence
a stranded pod.

`GET http://127.0.0.1:8099/status` returns cached control-plane health, both
lease expiries, and separate mapping/API diagnostics with HTTP 200 even when
degraded. It is intentionally localhost-only; do not expose it through an
Ingress or wire its degraded state to qBittorrent pod readiness. The image
runs as UID/GID 2000 with no writable filesystem requirement or capabilities.
At deployment, set `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`,
and drop `ALL` capabilities on this container; Dockerfile metadata alone cannot
enforce those runtime security options.
The status is **not** evidence of incoming peer delivery or leak protection.

From the repository root, install the pinned Go and Task versions and run the
same formatting, vet, and race-test checks used in CI:

```sh
mise install
task go:fmt
task go:check
```

Build from this directory:

```sh
GOOS=linux GOARCH=amd64 go build ./cmd/qbittorrent-natpmp-sync
GOOS=linux GOARCH=arm64 go build ./cmd/qbittorrent-natpmp-sync
docker buildx bake image-local
```

Before deployment, follow [PLAN.md](PLAN.md): back up qBittorrent settings,
validate the actual tunnel's fixed-port mapping and incoming TCP/UDP delivery,
then verify tracker/DHT announcements when the assigned public port changes.
API readback alone does not prove end-to-end reachability. Publishing an image
and the coordinated home-ops cutover are separate steps.

The NAT-PMP wire adapter is intentionally self-contained. The inspected
[`jackpal/go-nat-pmp`](https://github.com/jackpal/go-nat-pmp/blob/master/natpmp.go)
supports an explicit gateway but does not offer the cancellation and reply
validation needed here. Each exchange instead uses a connected UDP socket,
checks the reply opcode/result/ports/lifetime and has a bounded deadline.
