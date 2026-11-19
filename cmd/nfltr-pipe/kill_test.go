// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rand"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nfltr/relay-core/relaycoretest"
	"github.com/nfltr/relay/client/nfnet"
)

// TestMain runs the test binary as one end of a pipe when PIPE_CHILD says
// which (listen or dial), on its own stdin and stdout, against the relay
// PIPE_GRPC/PIPE_PROXY names: a peer a test can kill -9. PIPE_CHILD=cli:
// the command line nfltr-pipe, its relay that one (the README examples).
func TestMain(m *testing.M) {
	switch os.Getenv("PIPE_CHILD") {
	case "cli":
		viaRelay = func(c nfnet.Config) nfnet.Config {
			proxy := os.Getenv("PIPE_PROXY")
			c.Relay, c.AgentRelay, c.RelayTLS, c.Plaintext = "relay.test", os.Getenv("PIPE_GRPC"), nil, true
			c.DialRelay = func(ctx context.Context, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", proxy)
			}
			return c
		}
		os.Exit(cli(os.Args[1:]))
	case "listen":
		cfg := nfnet.Config{Relay: "relay.test", AgentRelay: os.Getenv("PIPE_GRPC"), Plaintext: true, Token: os.Getenv("PIPE_KEY")}
		os.Exit(listen(context.Background(), cfg, nfnet.DirectOn, cfg.Token == "", osStdio()))
	case "dial":
		proxy := os.Getenv("PIPE_PROXY")
		cfg := nfnet.Config{Relay: "relay.test", Plaintext: true, DialRelay: func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", proxy)
		}}
		t, err := nfnet.ParseTarget(os.Getenv("PIPE_ADDR"))
		if err != nil {
			os.Exit(1)
		}
		mode := nfnet.DirectOn
		if os.Getenv("PIPE_DIRECT") == "off" {
			mode = nfnet.DirectOff
		}
		os.Exit(dial(context.Background(), cfg, t, mode, osStdio()))
	}
	// No test reads this machine's nfltr config (its saved relay).
	_ = os.Setenv("NFLTR_CONFIG", filepath.Join(os.TempDir(), "nfltr-pipe-test-no-config", "nfltr.json"))
	os.Exit(m.Run())
}

// child starts the test binary as one end, its stdin endless random
// bytes; stderr goes to the returned pipe.
func child(t *testing.T, r *relaycoretest.Relay, role string, env ...string) (*exec.Cmd, io.Reader) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "PIPE_CHILD="+role, "PIPE_GRPC="+r.GRPC, "PIPE_PROXY="+r.Proxy)
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = rand.Reader
	cmd.Stdout = io.Discard
	er, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd, er
}

// counting is an end's stdout that says when the first bytes came.
type counting struct {
	n     chan struct{}
	first bool
}

func (c *counting) Write(p []byte) (int, error) {
	if !c.first {
		c.first = true
		close(c.n)
	}
	return len(p), nil
}

func (c *counting) Close() error { return nil }

// A peer killed mid-stream (kill -9: no reset sent, its sockets closed by
// its system) ends the other end's session, whichever end was killed,
// within the SDK's liveness bounds: relayed it exits 6 (reset); on the
// direct path of an address served with no account 12 (direct_required:
// the SDK reads a vanished peer as the direct path lost, which such an
// address cannot replace with a relayed one).
func TestKilledPeerIsTyped(t *testing.T) {
	for _, p := range paths {
		want := 6
		if p.anon {
			want = 12
		}
		t.Run("listener killed/"+p.name, func(t *testing.T) {
			r := testRelay(t)
			key := ""
			if !p.anon {
				key = r.Key
			}
			cmd, er := child(t, r, "listen", "PIPE_KEY="+key)
			addr := readAddr(t, er)
			go func() { _, _ = io.Copy(io.Discard, er) }()
			got := &counting{n: make(chan struct{})}
			tg, _ := nfnet.ParseTarget(addr)
			code := make(chan int, 1)
			go func() { code <- dial(t.Context(), dialCfg(r), tg, p.direct, stdio{rand.Reader, got, os.Stderr}) }()
			killWhenFlowing(t, cmd, got)
			waitWithin(t, "dial", code, want, 90*time.Second)
		})
		t.Run("dialler killed/"+p.name, func(t *testing.T) {
			r := testRelay(t)
			got := &counting{n: make(chan struct{})}
			lr, lw := io.Pipe()
			code := make(chan int, 1)
			go func() {
				code <- listen(t.Context(), listenCfg(r, p.anon), nfnet.DirectOn, p.anon, stdio{rand.Reader, got, lw})
			}()
			addr := readAddr(t, lr)
			go func() { _, _ = io.Copy(os.Stderr, lr) }()
			direct := "on"
			if p.direct == nfnet.DirectOff {
				direct = "off"
			}
			cmd, er := child(t, r, "dial", "PIPE_ADDR="+addr, "PIPE_DIRECT="+direct)
			go func() { _, _ = io.Copy(os.Stderr, er) }()
			killWhenFlowing(t, cmd, got)
			waitWithin(t, "listen", code, want, 90*time.Second)
		})
	}
}

func killWhenFlowing(t *testing.T, cmd *exec.Cmd, got *counting) {
	t.Helper()
	select {
	case <-got.n:
	case <-time.After(60 * time.Second):
		t.Fatal("no byte from the child")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
}

func waitWithin(t *testing.T, who string, code <-chan int, want int, d time.Duration) {
	t.Helper()
	t0 := time.Now()
	select {
	case c := <-code:
		if c != want {
			t.Fatalf("%s exited %d, want %d", who, c, want)
		}
		t.Logf("%s exited %d %v after the kill", who, c, time.Since(t0).Round(time.Millisecond))
	case <-time.After(d):
		t.Fatalf("%s still runs %v after its peer was killed", who, d)
	}
}
