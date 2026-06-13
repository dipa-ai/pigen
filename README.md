# pigen — a Pi Digit Generation Protocol server

An implementation of [**RFC 3091**](https://www.rfc-editor.org/rfc/rfc3091), the *Pi Digit Generation Protocol* (PIgen), published 1 April 2001. Like `chargen`, but instead of a rotating character pattern it streams the decimal digits of π — forever.

It's a joke RFC, so this is a joke service. It just happens to be a cloud-native, production-shaped, dependency-free one.

- 🥧 Streams π over **TCP** (continuous) and **UDP** (one datagram per request)
- 🌐 **Live browser view** at `/` — digits stream in over Server-Sent Events
- ♾️ Unbounded [spigot algorithm](https://en.wikipedia.org/wiki/Spigot_algorithm) (Gibbons, 2006) — digits computed on the fly, no precomputed table
- ☁️ 12-factor env config, JSON structured logs, `/healthz` + `/readyz` probes, `expvar` metrics, graceful shutdown
- 📦 Go **standard library only**, single source file, static binary, distroless container

> The RFC assigns port **314159**, which sadly does not fit in 16 bits. `pigen` therefore listens on **`:31415`** by default.

## Quick start

```bash
go run ./cmd/pigen      # or: make run
```

Then open <http://localhost:8080/> in a browser to watch the digits stream live, or, in another terminal:

```bash
# TCP — an endless stream of "3.14159265358979..."
nc localhost 31415 | head -c 50

# UDP — one datagram of digits per request
echo | nc -u -w1 localhost 31415

# The live web stream, raw (Server-Sent Events)
curl -N localhost:8080/stream

# HTTP — health, readiness, and metrics
curl localhost:8080/healthz
curl localhost:8080/readyz
curl localhost:8080/debug/vars
```

## Build

```bash
go build -o pigen ./cmd/pigen   # static binary, stdlib only — or: make build
go test ./...                   # spigot + payload tests — or: make test
```

### Docker

```bash
docker build -t pigen .
docker run --rm -p 31415:31415/tcp -p 31415:31415/udp -p 8080:8080 pigen
```

The image is a two-stage build onto `distroless/static` and runs as `nonroot`.

## Configuration

All configuration is via environment variables (12-factor):

| Variable | Default | Description |
| --- | --- | --- |
| `PIGEN_TCP_ADDR` | `:31415` | TCP listen address |
| `PIGEN_UDP_ADDR` | `:31415` | UDP listen address |
| `PIGEN_HTTP_ADDR` | `:8080` | HTTP address for probes & metrics |
| `PIGEN_MAX_CONNS` | `256` | Max concurrent TCP connections (excess is shed) |
| `PIGEN_MAX_DIGITS` | `0` | Digits per TCP connection; **`0` = unlimited**, per the RFC |
| `PIGEN_UDP_DIGITS` | `64` | Digits per UDP reply (clamped to 1–500) |
| `PIGEN_WRITE_TIMEOUT_SEC` | `30` | Per-write deadline |
| `PIGEN_WEB_PACE_MS` | `75` | Pause between digits on the browser (SSE) stream |
| `PIGEN_LEGACY_PI` | `false` | When `true`, π is emitted as exactly `3` — for jurisdictions inspired by the 1897 [Indiana Pi Bill](https://en.wikipedia.org/wiki/Indiana_Pi_Bill) |

## Metrics

`GET /debug/vars` exposes stdlib `expvar` counters:

| Counter | Meaning |
| --- | --- |
| `pigen_tcp_connections_total` | TCP connections accepted |
| `pigen_tcp_connections_active` | TCP connections currently open |
| `pigen_udp_packets_total` | UDP datagrams received |
| `pigen_web_clients_active` | Browser (SSE) streams currently open |
| `pigen_digits_sent_total` | Total π digits written (TCP + UDP + web) |

## Deploy (Kubernetes + Envoy Gateway, GitOps)

Manifests live in [`kubernetes/`](kubernetes/) as a kustomize base — point your GitOps tool (Argo/Flux) at that path, or apply directly:

```bash
kubectl apply -k kubernetes/
```

It ships:

- **`deployment.yaml`** — 2 replicas, both probes on the HTTP port, hardened `securityContext` (non-root, read-only root FS, all caps dropped), `PIGEN_MAX_DIGITS` capped for shared clusters.
- **`service.yaml`** — exposes `pi-tcp` (31415), `pi-udp` (31415) and `http` (8080).
- **`httproute.yaml`** — `HTTPRoute` for the web view, attached to your Gateway's HTTPS listener.
- **`tcproute.yaml`** — `TCPRoute` exposing the raw RFC-3091 stream over a dedicated TCP listener.

The base ships generic placeholders: image `pigen:latest`, hostname `pigen.example.com`, and Gateway refs `gateway`/`https` (web) and `gateway`/`tcp` (raw π). Override them for your environment with your GitOps tool — kustomize `images` for the image, `patches` for the hostname and the routes' `parentRefs` — rather than editing the base.

### Wiring the routes to Envoy Gateway

**Web (L7).** The `HTTPRoute` attaches to an HTTPS listener on your Gateway and routes by hostname — nothing special needed beyond a TLS cert for `pigen.my-domain.com`.

**Raw π (L4).** Add a dedicated TCP listener to your Gateway (TCP is L4 — it carries no hostname, so this is a bare port on the Gateway address):

```bash
kubectl patch gateway eg --type=json -p '[{
  "op": "add", "path": "/spec/listeners/-",
  "value": {"name": "pi-tcp", "protocol": "TCP", "port": 31415,
            "allowedRoutes": {"kinds": [{"kind": "TCPRoute"}]}}
}]'
```

Then point a DNS **A-record** `pigen.my-domain.com` → the Gateway's IP. With that, `nc pigen.my-domain.com 31415` streams π (the name resolves via DNS; routing is by port).

> **SSE note.** If the browser stream gets cut after a while, Envoy's route timeout is the usual culprit — disable it for the stream with a `BackendTrafficPolicy`:
>
> ```yaml
> apiVersion: gateway.envoyproxy.io/v1alpha1
> kind: BackendTrafficPolicy
> metadata: {name: pigen-no-timeout}
> spec:
>   targetRefs:
>     - {group: gateway.networking.k8s.io, kind: HTTPRoute, name: pigen}
>   timeout:
>     http: {requestTimeout: "0s"}
> ```

## How it works

The digits come from an unbounded spigot algorithm running over `math/big`. Each digit advances the generator's state, and that state grows without bound — so generation gradually slows down, which conveniently doubles as a natural rate limiter. Each TCP connection — and each browser stream — gets its own generator (it isn't concurrency-safe); the UDP reply is computed once at startup and shared, since every client wants the same leading digits anyway. The browser view (`/`) is a self-contained page served inline from the binary; it subscribes to `/stream` (Server-Sent Events) and the server paces digits out one per `PIGEN_WEB_PACE_MS`.

## License

A toy. Use it for whatever brings you joy.

---

*Created by [@schors](https://github.com/schors) with Claude Fable*
