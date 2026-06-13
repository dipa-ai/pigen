# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`pigen` implements **RFC 3091 — the Pi Digit Generation Protocol** (a 1 April 2001 joke RFC): a chargen-like service that streams the decimal digits of pi over TCP and UDP, plus a live browser view. All logic lives in one source file, `cmd/pigen/main.go`, module `pigen` (`go.mod` at the repo root), Go 1.26, **stdlib only** (no third-party deps, hence the bare `go.mod`). Keeping it to a single source file is deliberate.

The RFC's port 314159 doesn't fit in 16 bits, so the service defaults to `:31415` for both TCP and UDP, plus `:8080` for the web view, probes, and metrics.

## Commands

```bash
go build -o pigen ./cmd/pigen   # build static binary (or: make build)
go test ./...                   # spigot + udpPayload tests (or: make test)
go vet ./...                    # vet
gofmt -l .                      # list unformatted files; make fmt-check fails on output
go run ./cmd/pigen              # run locally (TCP+UDP :31415, HTTP :8080)
```

A `Makefile` wraps these (`build`/`test`/`vet`/`fmt`/`fmt-check`/`run`/`image`). Tests cover the spigot (first 100 digits of pi) and `udpPayload`; the `TestSpigotDigits` reference string is the natural place to catch any regression in the digit algorithm.

Smoke-test a running instance:
```bash
nc localhost 31415 | head -c 50          # TCP: streaming "3.14159..."
echo | nc -u -w1 localhost 31415         # UDP: one datagram of digits
curl -N localhost:8080/stream            # web SSE stream (raw)
curl localhost:8080/readyz               # readiness probe
curl localhost:8080/debug/vars           # expvar metrics
# or open http://localhost:8080/ in a browser for the live view
```

Container / deploy: `docker build` uses a two-stage distroless `nonroot` image (`Dockerfile`, builds `./cmd/pigen`). Manifests are a kustomize base in `kubernetes/` (Deployment + Service + Gateway API `HTTPRoute`/`TCPRoute`) — see the README's Deploy section for the Envoy Gateway wiring (TCP listener patch + DNS).

## Architecture

`cmd/pigen/main.go` is divided by banner comments into independent sections. The key relationships:

- **Spigot generator** (`spigot`, `newSpigot`, `next`): Gibbons (2006) unbounded spigot algorithm over `math/big`. `next()` returns one ASCII digit and **mutates the spigot in place — it is not safe for concurrent use**, so every TCP connection *and every web stream* calls `newSpigot()` to get its own. State grows without bound as digits are produced, so generation naturally slows over time; this is intentional and acts as a built-in rate limiter.

- **TCP** (`serveTCP` → `handleTCP`): accept loop guards concurrency with a buffered-channel semaphore sized to `maxConns` and **sheds load** (closes the connection) when full rather than blocking. Each connection streams `"3.14159…"` forever until the client disconnects (a write error is the normal, expected termination per the RFC).

- **UDP** (`serveUDP`, `udpPayload`): the reply payload is computed **once at startup** and the same bytes are sent to every datagram — UDP clients all receive the identical leading N digits by design (contrast with TCP's per-connection spigot). `udpDigits` is clamped to 1–500 to stay within one datagram.

- **Web** (`httpServer`, `handleStream`, `indexHTML`): `GET /` serves a self-contained page (HTML/CSS/JS inline in the `indexHTML` const — the JS deliberately avoids backticks since the const is a Go raw string). `GET /stream` is the SSE endpoint: own `newSpigot()`, paces one digit per `webPace` tick, and `select`s on **both** `r.Context().Done()` (client gone) and the base `ctx` (server shutdown) — which is why `httpServer` takes the `ctx`, so SIGTERM ends in-flight streams instead of stalling `Shutdown` to its 10s timeout.

- **HTTP probes/metrics** (same `httpServer`): `/healthz` (liveness, always OK), `/readyz` (gated by the `ready` atomic.Bool — flipped true after listeners bind, false on shutdown), and `/debug/vars` (stdlib `expvar` counters: `pigen_tcp_connections_total/active`, `pigen_udp_packets_total`, `pigen_web_clients_active`, `pigen_digits_sent_total`).

- **`main`**: binds listeners, launches TCP/UDP/HTTP goroutines tracked by a `sync.WaitGroup`, then blocks on a `signal.NotifyContext` (SIGINT/SIGTERM). On signal it flips `ready` false, closes listeners (which unblocks the accept/read loops via `net.ErrClosed`), and does a graceful HTTP shutdown bounded by a 10s timeout.

## Configuration (12-factor, env vars)

All read in `loadConfig`. Defaults in parentheses:

- `PIGEN_TCP_ADDR` (`:31415`), `PIGEN_UDP_ADDR` (`:31415`), `PIGEN_HTTP_ADDR` (`:8080`)
- `PIGEN_MAX_CONNS` (`256`) — concurrent TCP cap
- `PIGEN_MAX_DIGITS` (`0`) — digits per TCP connection; **0 = unlimited, per the RFC**
- `PIGEN_UDP_DIGITS` (`64`) — digits per UDP reply (clamped 1–500)
- `PIGEN_WRITE_TIMEOUT_SEC` (`30`)
- `PIGEN_WEB_PACE_MS` (`75`) — pause between digits on the browser SSE stream
- `PIGEN_LEGACY_PI` (`false`) — when `true`, pi is emitted as exactly `"3"` (a nod to the 1897 Indiana Pi Bill); short-circuits the TCP, UDP, *and* web paths.

When changing defaults or env-var names, update `kubernetes/deployment.yaml` (it sets `PIGEN_MAX_DIGITS` and `PIGEN_WEB_PACE_MS`) and the `EXPOSE`d ports in `Dockerfile` to match.
