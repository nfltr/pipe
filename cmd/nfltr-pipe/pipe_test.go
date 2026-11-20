// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nfltr/relay-core/relaycoretest"
	"github.com/nfltr/relay/client/nfnet"
	"github.com/spf13/viper"
)

// testRelay is a relay core taking nf1 addresses (host relay.test) and
// addresses served with no account, as send's tests run one.
func testRelay(t *testing.T) *relaycoretest.Relay {
	set := map[string]any{"nf1.enabled": true, "nf1.hosts": []string{"relay.test"}, "nf1.anon.enabled": true}
	for k, v := range set {
		viper.Set(k, v)
	}
	t.Cleanup(func() { // after the relay's own cleanup
		viper.Set("nf1.enabled", false)
		viper.Set("nf1.hosts", nil)
		viper.Set("nf1.anon.enabled", false)
	})
	return relaycoretest.New(t)
}

// path is how a test's session goes: direct (the listener served with no
// account: no relayed path exists) or relayed (an account's listener, a
// dial with direct paths off).
type path struct {
	name   string
	anon   bool
	direct nfnet.DirectMode
}

var paths = []path{{"direct", true, nfnet.DirectOn}, {"relayed", false, nfnet.DirectOff}}

func listenCfg(r *relaycoretest.Relay, anon bool) nfnet.Config {
	cfg := nfnet.Config{Relay: "relay.test", AgentRelay: r.GRPC, Plaintext: true}
	if !anon {
		cfg.Token = r.Key
	}
	return cfg
}

func dialCfg(r *relaycoretest.Relay) nfnet.Config {
	return nfnet.Config{Relay: "relay.test", Plaintext: true, DialRelay: func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", r.Proxy)
	}}
}

// out is an end's stdout: what it got, and whether it was closed.
type out struct {
	mu     sync.Mutex
	b      bytes.Buffer
	closed chan struct{}
	fail   error // every write fails with it
}

func newOut() *out { return &out{closed: make(chan struct{})} }

func (o *out) Write(p []byte) (int, error) {
	if o.fail != nil {
		return 0, o.fail
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *out) Close() error { close(o.closed); return nil }

func (o *out) bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return bytes.Clone(o.b.Bytes())
}

// startListen runs listen with in, returning its address, stdout and exit
// status.
func startListen(t *testing.T, ctx context.Context, r *relaycoretest.Relay, p path, in io.Reader, o *out) (string, <-chan int) {
	t.Helper()
	lr, lw := io.Pipe()
	code := make(chan int, 1)
	go func() {
		code <- listen(ctx, listenCfg(r, p.anon), nfnet.DirectOn, p.anon, stdio{in, o, lw})
		_ = lw.Close()
	}()
	addr := readAddr(t, lr)
	go func() { _, _ = io.Copy(os.Stderr, lr) }()
	return addr, code
}

func readAddr(t *testing.T, r io.Reader) string {
	t.Helper()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if l := sc.Text(); strings.HasPrefix(l, "nf1") {
			return l
		}
	}
	t.Fatal("no address")
	return ""
}

func startDial(t *testing.T, ctx context.Context, r *relaycoretest.Relay, addr string, mode nfnet.DirectMode, in io.Reader, o *out) <-chan int {
	t.Helper()
	tg, err := nfnet.ParseTarget(addr)
	if err != nil {
		t.Fatal(err)
	}
	code := make(chan int, 1)
	go func() { code <- dial(ctx, dialCfg(r), tg, mode, stdio{in, o, os.Stderr}) }()
	return code
}

func wait(t *testing.T, who string, code <-chan int, want int) {
	t.Helper()
	select {
	case c := <-code:
		if c != want {
			t.Fatalf("%s exited %d, want %d", who, c, want)
		}
	case <-time.After(90 * time.Second):
		t.Fatalf("%s did not exit", who)
	}
}

func random(t *testing.T, n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// Every byte arrives, both ways at once, exactly, on each path; a side
// with an empty stdin as well (its peer's tail must not be lost when it
// exits at once), and both exit 0.
func TestRoundTrip(t *testing.T) {
	for _, p := range paths {
		for _, sz := range [][2]int{{8 << 20, 3 << 20}, {16 << 20, 0}, {0, 16 << 20}, {1, 1}} {
			t.Run(p.name, func(t *testing.T) {
				r := testRelay(t)
				a, b := random(t, sz[0]), random(t, sz[1])
				la, da := newOut(), newOut()
				addr, lc := startListen(t, t.Context(), r, p, bytes.NewReader(a), la)
				dc := startDial(t, t.Context(), r, addr, p.direct, bytes.NewReader(b), da)
				wait(t, "dial", dc, 0)
				wait(t, "listen", lc, 0)
				if !bytes.Equal(da.bytes(), a) {
					t.Errorf("dial got %d bytes of listen's %d, not the same", len(da.bytes()), len(a))
				}
				if !bytes.Equal(la.bytes(), b) {
					t.Errorf("listen got %d bytes of dial's %d, not the same", len(la.bytes()), len(b))
				}
			})
		}
	}
}

// A half close: dial's stdin ends; listen's stdout gets it whole and
// closed while listen's stdin is still open, and what listen sends after
// that still reaches dial; both exit 0 once listen's stdin ends too.
func TestHalfClose(t *testing.T) {
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			r := testRelay(t)
			pr, pw := io.Pipe()
			la, da := newOut(), newOut()
			addr, lc := startListen(t, t.Context(), r, p, pr, la)
			msg := random(t, 1<<20)
			dc := startDial(t, t.Context(), r, addr, p.direct, bytes.NewReader(msg), da)
			select {
			case <-la.closed:
			case <-time.After(60 * time.Second):
				t.Fatal("listen's stdout not closed after dial's stdin ended")
			}
			if !bytes.Equal(la.bytes(), msg) {
				t.Fatalf("listen got %d bytes, want dial's %d", len(la.bytes()), len(msg))
			}
			late := random(t, 2<<20)
			if _, err := pw.Write(late); err != nil {
				t.Fatal(err)
			}
			_ = pw.Close()
			wait(t, "dial", dc, 0)
			wait(t, "listen", lc, 0)
			if !bytes.Equal(da.bytes(), late) {
				t.Fatalf("dial got %d bytes after its half close, want %d", len(da.bytes()), len(late))
			}
		})
	}
}

// An end whose stdout fails resets the session: it exits 6 and so does
// the other end, which never reads an end of its stdout.
func TestLocalFailureResets(t *testing.T) {
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			r := testRelay(t)
			pr, _ := io.Pipe() // both stdins stay open
			lin, _ := io.Pipe()
			la, da := newOut(), newOut()
			la.fail = errors.New("broken pipe")
			addr, lc := startListen(t, t.Context(), r, p, lin, la)
			dc := startDial(t, t.Context(), r, addr, p.direct, io.MultiReader(bytes.NewReader(random(t, 1<<20)), pr), da)
			wait(t, "listen", lc, 6)
			wait(t, "dial", dc, 6)
			select {
			case <-da.closed:
				t.Fatal("dial's stdout closed as at an end")
			default:
			}
		})
	}
}

// A stopped end (SIGINT, SIGTERM) resets the session: both exit 6.
func TestStoppedEndResets(t *testing.T) {
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			r := testRelay(t)
			lin, _ := io.Pipe()
			din, _ := io.Pipe()
			ctx, stop := context.WithCancel(t.Context())
			la, da := newOut(), newOut()
			addr, lc := startListen(t, t.Context(), r, p, lin, la)
			dc := startDial(t, ctx, r, addr, p.direct, din, da)
			time.Sleep(500 * time.Millisecond) // the session open; either way the outcome is 6
			stop()
			wait(t, "dial", dc, 6)
			wait(t, "listen", lc, 6)
		})
	}
}

// An address served with no account and a dial with no direct path: 12.
// A malformed address: 1. An address nobody serves: 3.
func TestOpenFailuresAreTyped(t *testing.T) {
	r := testRelay(t)
	addr, _ := startListen(t, t.Context(), r, paths[0], bytes.NewReader(nil), newOut())
	wait(t, "dial --direct off", startDial(t, t.Context(), r, addr, nfnet.DirectOff, bytes.NewReader(nil), newOut()), 12)
	if code := dialCmd(t.Context(), []string{"nf1notanaddress"}); code != 1 {
		t.Errorf("a malformed address: exit %d, want 1", code)
	}
	ctx, stop := context.WithCancel(t.Context())
	gone, lc := startListen(t, ctx, r, paths[1], bytes.NewReader(nil), newOut())
	stop()
	<-lc
	wait(t, "dial of a closed address", startDial(t, t.Context(), r, gone, nfnet.DirectOff, bytes.NewReader(nil), newOut()), 3)
}

// A second dial of an address whose session runs is reset: it exits 6
// (never 10, which says the address's secret was refused: defect 3,
// docs/reliability.md) and the first session completes, on each path and
// with an account's listener taking direct paths (the matrix's direct
// cells).
func TestSecondDialIsReset(t *testing.T) {
	for _, p := range append(paths, path{"account-direct", false, nfnet.DirectOn}) {
		t.Run(p.name, func(t *testing.T) {
			r := testRelay(t)
			lin, lw := io.Pipe()
			din, dw := io.Pipe()
			la, da := newOut(), newOut()
			addr, lc := startListen(t, t.Context(), r, p, lin, la)
			dc := startDial(t, t.Context(), r, addr, p.direct, din, da)
			if _, err := dw.Write([]byte("x")); err != nil { // the first session open
				t.Fatal(err)
			}
			wait(t, "second dial", startDial(t, t.Context(), r, addr, p.direct, bytes.NewReader(nil), newOut()), 6)
			_ = lw.Close()
			_ = dw.Close()
			wait(t, "dial", dc, 0)
			wait(t, "listen", lc, 0)
		})
	}
}
