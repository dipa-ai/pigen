# pigen — a Pi Digit Generation Protocol server

An implementation of [**RFC 3091**](https://www.rfc-editor.org/rfc/rfc3091), the *Pi Digit Generation Protocol* (PIgen), published 1 April 2001. Like `chargen`, but instead of a rotating character pattern it streams the decimal digits of π — forever.

It's a joke RFC, so this is a joke service. It just happens to be a cloud-native, production-shaped, dependency-free one.

- 🥧 Streams π over **TCP** (continuous) and **UDP** (one datagram per request)
- ♾️ Unbounded [spigot algorithm](https://en.wikipedia.org/wiki/Spigot_algorithm) (Gibbons, 2006) — digits computed on the fly, no precomputed table
- ☁️ 12-factor env config, JSON structured logs, `/healthz` + `/readyz` probes, `expvar` metrics, graceful shutdown
- 📦 Go **standard library only**, single file, static binary, distroless container

> The RFC assigns port **314159**, which sadly does not fit in 16 bits. `pigen` therefore listens on **`:31415`** by default.

## Quick start

```bash
go run .
```

Then, in another terminal:

```bash
# TCP — an endless stream of "3.14159265358979..."
nc localhost 31415 | head -c 50

# UDP — one datagram of digits per request
echo | nc -u -w1 localhost 31415

# HTTP — health, readiness, and metrics
curl localhost:8080/healthz
curl localhost:8080/readyz
curl localhost:8080/debug/vars
```

## Build

```bash
go build -o pigen .   # static binary, stdlib only
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
| `PIGEN_LEGACY_PI` | `false` | When `true`, π is emitted as exactly `3` — for jurisdictions inspired by the 1897 [Indiana Pi Bill](https://en.wikipedia.org/wiki/Indiana_Pi_Bill) |

## Metrics

`GET /debug/vars` exposes stdlib `expvar` counters:

| Counter | Meaning |
| --- | --- |
| `pigen_tcp_connections_total` | TCP connections accepted |
| `pigen_tcp_connections_active` | TCP connections currently open |
| `pigen_udp_packets_total` | UDP datagrams received |
| `pigen_digits_sent_total` | Total π digits written |

## Kubernetes

`k8s.yaml` ships a 2-replica `Deployment` + `Service` with both probes wired to the HTTP port, a hardened `securityContext` (non-root, read-only root FS, all capabilities dropped), and `PIGEN_MAX_DIGITS` capped for shared clusters. Point the image at your registry and apply:

```bash
kubectl apply -f k8s.yaml
```

## How it works

The digits come from an unbounded spigot algorithm running over `math/big`. Each digit advances the generator's state, and that state grows without bound — so generation gradually slows down, which conveniently doubles as a natural rate limiter. Each TCP connection gets its own generator (it isn't concurrency-safe); the UDP reply is computed once at startup and shared, since every client wants the same leading digits anyway.

## License

A toy. Use it for whatever brings you joy.

---

*Created by [@schors](https://github.com/schors) with claude fable*
