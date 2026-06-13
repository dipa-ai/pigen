// Command pigen implements RFC 3091 — Pi Digit Generation Protocol (PIgen).
//
// The RFC (1 April 2001) specifies a chargen-like service streaming the
// decimal digits of pi over TCP and UDP on port 314159. Since that port
// number does not fit into 16 bits, the listen address is configurable
// and defaults to :31415.
//
// Cloud-native bits: 12-factor env config, JSON structured logging (slog),
// /healthz + /readyz probes, expvar metrics at /debug/vars, graceful
// shutdown on SIGTERM/SIGINT, stdlib only, static binary.
package main

import (
	"context"
	"errors"
	"expvar"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ---------------------------------------------------------------------------
// Unbounded spigot algorithm for pi (Gibbons, 2006). Yields decimal digits
// one by one, forever; state grows, so generation gradually slows down,
// which conveniently acts as a natural rate limiter.
// ---------------------------------------------------------------------------

var (
	big1  = big.NewInt(1)
	big2  = big.NewInt(2)
	big3  = big.NewInt(3)
	big4  = big.NewInt(4)
	big7  = big.NewInt(7)
	big10 = big.NewInt(10)
)

type spigot struct {
	q, r, t, k, n, l *big.Int
}

func newSpigot() *spigot {
	return &spigot{
		q: big.NewInt(1), r: big.NewInt(0), t: big.NewInt(1),
		k: big.NewInt(1), n: big.NewInt(3), l: big.NewInt(3),
	}
}

// next returns the next decimal digit of pi as an ASCII byte.
func (s *spigot) next() byte {
	for {
		// if 4q + r - t < n*t  =>  emit n
		lhs := new(big.Int).Mul(big4, s.q)
		lhs.Add(lhs, s.r)
		nt := new(big.Int).Mul(s.n, s.t)
		if lhs.Cmp(new(big.Int).Add(nt, s.t)) < 0 {
			d := byte('0' + s.n.Int64())
			// (q, r, n) <- (10q, 10(r - n*t), (10(3q + r))/t - 10n)
			newR := new(big.Int).Sub(s.r, nt)
			newR.Mul(newR, big10)
			newN := new(big.Int).Mul(big3, s.q)
			newN.Add(newN, s.r)
			newN.Mul(newN, big10)
			newN.Quo(newN, s.t)
			newN.Sub(newN, new(big.Int).Mul(big10, s.n))
			s.q.Mul(s.q, big10)
			s.r, s.n = newR, newN
			return d
		}
		// (q, r, t, k, n, l) <- (qk, (2q+r)l, tl, k+1, (q(7k+2)+rl)/(tl), l+2)
		newR := new(big.Int).Mul(big2, s.q)
		newR.Add(newR, s.r)
		newR.Mul(newR, s.l)
		newT := new(big.Int).Mul(s.t, s.l)
		newN := new(big.Int).Mul(big7, s.k)
		newN.Add(newN, big2)
		newN.Mul(newN, s.q)
		newN.Add(newN, new(big.Int).Mul(s.r, s.l))
		newN.Quo(newN, newT)
		s.q.Mul(s.q, s.k)
		s.r, s.t, s.n = newR, newT, newN
		s.k.Add(s.k, big1)
		s.l.Add(s.l, big2)
	}
}

// ---------------------------------------------------------------------------
// Configuration (env, 12-factor)
// ---------------------------------------------------------------------------

type config struct {
	tcpAddr      string
	udpAddr      string
	httpAddr     string
	maxConns     int64
	maxDigits    uint64 // per TCP connection; 0 = unlimited, per the RFC
	udpDigits    int    // digits per UDP reply
	writeTimeout time.Duration
	legacyPi     bool // jurisdictions where pi is legislated to be 3
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func loadConfig() config {
	return config{
		tcpAddr:      envStr("PIGEN_TCP_ADDR", ":31415"),
		udpAddr:      envStr("PIGEN_UDP_ADDR", ":31415"),
		httpAddr:     envStr("PIGEN_HTTP_ADDR", ":8080"),
		maxConns:     envInt("PIGEN_MAX_CONNS", 256),
		maxDigits:    uint64(envInt("PIGEN_MAX_DIGITS", 0)),
		udpDigits:    int(envInt("PIGEN_UDP_DIGITS", 64)),
		writeTimeout: time.Duration(envInt("PIGEN_WRITE_TIMEOUT_SEC", 30)) * time.Second,
		legacyPi:     os.Getenv("PIGEN_LEGACY_PI") == "true",
	}
}

// ---------------------------------------------------------------------------
// Metrics (stdlib expvar, exposed at /debug/vars)
// ---------------------------------------------------------------------------

var (
	tcpConnsTotal  = expvar.NewInt("pigen_tcp_connections_total")
	tcpConnsActive = expvar.NewInt("pigen_tcp_connections_active")
	udpPacketsIn   = expvar.NewInt("pigen_udp_packets_total")
	digitsSent     = expvar.NewInt("pigen_digits_sent_total")
	ready          atomic.Bool
)

// ---------------------------------------------------------------------------
// TCP: stream "3.14159..." until the client disconnects (RFC 3091 §TCP)
// ---------------------------------------------------------------------------

func handleTCP(ctx context.Context, conn net.Conn, cfg config, log *slog.Logger) {
	defer conn.Close()
	tcpConnsTotal.Add(1)
	tcpConnsActive.Add(1)
	defer tcpConnsActive.Add(-1)

	log.Info("tcp connection", "remote", conn.RemoteAddr().String())

	if cfg.legacyPi {
		// Where pi is legislated to be exactly 3 (cf. the 1897 Indiana Pi
		// Bill), the full digit stream is rather short.
		conn.SetWriteDeadline(time.Now().Add(cfg.writeTimeout))
		if _, err := conn.Write([]byte("3")); err == nil {
			digitsSent.Add(1)
		}
		return
	}

	s := newSpigot()
	buf := make([]byte, 0, 256)
	var sent uint64
	first := true

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		buf = buf[:0]
		for len(buf) < 256 {
			buf = append(buf, s.next())
			sent++
			if first {
				buf = append(buf, '.')
				first = false
			}
			if cfg.maxDigits > 0 && sent >= cfg.maxDigits {
				break
			}
		}

		conn.SetWriteDeadline(time.Now().Add(cfg.writeTimeout))
		if _, err := conn.Write(buf); err != nil {
			return // client went away — normal termination per the RFC
		}
		digitsSent.Add(int64(len(buf)))

		if cfg.maxDigits > 0 && sent >= cfg.maxDigits {
			return
		}
	}
}

func serveTCP(ctx context.Context, ln net.Listener, cfg config, log *slog.Logger, wg *sync.WaitGroup) {
	defer wg.Done()
	sem := make(chan struct{}, cfg.maxConns)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Error("accept", "err", err)
			continue
		}
		select {
		case sem <- struct{}{}:
		default:
			conn.Close() // over capacity — shed load
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			handleTCP(ctx, conn, cfg, log)
		}()
	}
}

// ---------------------------------------------------------------------------
// UDP: any datagram is answered with a datagram of pi digits (RFC 3091 §UDP)
// ---------------------------------------------------------------------------

func udpPayload(cfg config) []byte {
	if cfg.legacyPi {
		return []byte("3")
	}
	n := cfg.udpDigits
	if n < 1 {
		n = 1
	}
	if n > 500 { // keep well within a single datagram
		n = 500
	}
	s := newSpigot()
	out := make([]byte, 0, n+1)
	for i := 0; i < n; i++ {
		out = append(out, s.next())
		if i == 0 {
			out = append(out, '.')
		}
	}
	return out
}

func serveUDP(ctx context.Context, pc net.PacketConn, payload []byte, log *slog.Logger, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 1500)
	for {
		_, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Error("udp read", "err", err)
			continue
		}
		udpPacketsIn.Add(1)
		if _, err := pc.WriteTo(payload, addr); err == nil {
			digitsSent.Add(int64(len(payload)))
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP: probes + metrics
// ---------------------------------------------------------------------------

func httpServer(cfg config) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ready.Load() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ready\n"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("not ready\n"))
	})
	mux.Handle("/debug/vars", expvar.Handler())
	return &http.Server{Addr: cfg.httpAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}

// ---------------------------------------------------------------------------

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	cfg := loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tcpLn, err := net.Listen("tcp", cfg.tcpAddr)
	if err != nil {
		log.Error("tcp listen", "err", err)
		os.Exit(1)
	}
	udpConn, err := net.ListenPacket("udp", cfg.udpAddr)
	if err != nil {
		log.Error("udp listen", "err", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go serveTCP(ctx, tcpLn, cfg, log, &wg)
	go serveUDP(ctx, udpConn, udpPayload(cfg), log, &wg)

	srv := httpServer(cfg)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
		}
	}()

	ready.Store(true)
	log.Info("pigen up (RFC 3091)",
		"tcp", cfg.tcpAddr, "udp", cfg.udpAddr, "http", cfg.httpAddr,
		"legacy_pi", cfg.legacyPi)

	<-ctx.Done()
	ready.Store(false)
	log.Info("shutting down")

	tcpLn.Close()
	udpConn.Close()

	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shCtx)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-shCtx.Done():
		log.Warn("shutdown timeout, exiting anyway")
	}
	log.Info("bye")
}
