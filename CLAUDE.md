# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`pigen` implements **RFC 3091 — the Pi Digit Generation Protocol** (a 1 April 2001 joke RFC): a chargen-like service that streams the decimal digits of pi over TCP and UDP. The whole program is one file, `main.go`, module `pigen`, Go 1.22, **stdlib only** (no third-party deps, hence the bare `go.mod`).

The RFC's port 314159 doesn't fit in 16 bits, so the service defaults to `:31415` for both TCP and UDP, plus `:8080` for HTTP probes/metrics.

## Commands

```bash
go build -o pigen .        # build static binary
go vet ./...               # vet
gofmt -l .                 # list unformatted files (CI-style check)
go run .                   # run locally (TCP+UDP :31415, HTTP :8080)
```

There are currently **no tests** — `go test ./...` reports "no test files". If you add logic, the spigot (`next()`) and `udpPayload`/config parsing are the natural units to cover.

Smoke-test a running instance:
```bash
nc localhost 31415 | head -c 50          # TCP: streaming "3.14159..."
echo | nc -u -w1 localhost 31415         # UDP: one datagram of digits
curl localhost:8080/readyz               # readiness probe
curl localhost:8080/debug/vars           # expvar metrics
```

Container / deploy: `docker build` uses a two-stage distroless `nonroot` image (`Dockerfile`); `k8s.yaml` is a Deployment + Service with both probes wired to the HTTP port.

## Architecture

`main.go` is divided by banner comments into independent sections. The key relationships:

- **Spigot generator** (`spigot`, `newSpigot`, `next`): Gibbons (2006) unbounded spigot algorithm over `math/big`. `next()` returns one ASCII digit and **mutates the spigot in place — it is not safe for concurrent use**, so every TCP connection calls `newSpigot()` to get its own. State grows without bound as digits are produced, so generation naturally slows over time; this is intentional and acts as a built-in rate limiter.

- **TCP** (`serveTCP` → `handleTCP`): accept loop guards concurrency with a buffered-channel semaphore sized to `maxConns` and **sheds load** (closes the connection) when full rather than blocking. Each connection streams `"3.14159…"` forever until the client disconnects (a write error is the normal, expected termination per the RFC).

- **UDP** (`serveUDP`, `udpPayload`): the reply payload is computed **once at startup** and the same bytes are sent to every datagram — UDP clients all receive the identical leading N digits by design (contrast with TCP's per-connection spigot). `udpDigits` is clamped to 1–500 to stay within one datagram.

- **HTTP** (`httpServer`): `/healthz` (liveness, always OK), `/readyz` (gated by the `ready` atomic.Bool — flipped true after listeners bind, false on shutdown), and `/debug/vars` (stdlib `expvar` counters: `pigen_tcp_connections_total/active`, `pigen_udp_packets_total`, `pigen_digits_sent_total`).

- **`main`**: binds listeners, launches TCP/UDP/HTTP goroutines tracked by a `sync.WaitGroup`, then blocks on a `signal.NotifyContext` (SIGINT/SIGTERM). On signal it flips `ready` false, closes listeners (which unblocks the accept/read loops via `net.ErrClosed`), and does a graceful HTTP shutdown bounded by a 10s timeout.

## Configuration (12-factor, env vars)

All read in `loadConfig`. Defaults in parentheses:

- `PIGEN_TCP_ADDR` (`:31415`), `PIGEN_UDP_ADDR` (`:31415`), `PIGEN_HTTP_ADDR` (`:8080`)
- `PIGEN_MAX_CONNS` (`256`) — concurrent TCP cap
- `PIGEN_MAX_DIGITS` (`0`) — digits per TCP connection; **0 = unlimited, per the RFC**
- `PIGEN_UDP_DIGITS` (`64`) — digits per UDP reply (clamped 1–500)
- `PIGEN_WRITE_TIMEOUT_SEC` (`30`)
- `PIGEN_LEGACY_PI` (`false`) — when `true`, pi is emitted as exactly `"3"` (a nod to the 1897 Indiana Pi Bill); short-circuits both TCP and UDP paths.

When changing defaults or env-var names, update `k8s.yaml` (it sets `PIGEN_MAX_DIGITS`) and the `EXPOSE`d ports in `Dockerfile` to match.
