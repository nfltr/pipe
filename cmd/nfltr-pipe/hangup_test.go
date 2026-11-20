// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package main

import (
	"crypto/rand"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/nfltr/relay/client/nfnet"
)

// A hangup (SIGHUP: ssh stops its ProxyCommand so when it closes, a
// terminal closing does it to its jobs) resets the session like any other
// stop: the hung up end exits 6 and so does the other at once, not after
// the SDK's liveness (on a direct path served with no account, as 12).
func TestHangupResets(t *testing.T) {
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			r := testRelay(t)
			got := &counting{n: make(chan struct{})}
			lr, lw := io.Pipe()
			code := make(chan int, 1)
			go func() {
				code <- listen(t.Context(), listenCfg(r, p.anon), nfnet.DirectOn, p.anon, stdio{rand.Reader, got, lw})
			}()
			addr := readAddr(t, lr)
			go func(w io.Writer) { _, _ = io.Copy(w, lr) }(os.Stderr)
			args := []string{"dial", addr}
			if p.direct == nfnet.DirectOff {
				args = []string{"dial", "--direct", "off", addr}
			}
			cmd := exec.Command(os.Args[0], args...)
			cmd.Env = append(os.Environ(), "PIPE_CHILD=cli", "PIPE_GRPC="+r.GRPC, "PIPE_PROXY="+r.Proxy)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = rand.Reader, io.Discard, os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			dc := waiter(cmd)
			select {
			case <-got.n:
			case <-time.After(60 * time.Second):
				t.Fatal("no byte from the dialler")
			}
			if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
				t.Fatal(err)
			}
			waitWithin(t, "dial", dc, 6, 10*time.Second)
			waitWithin(t, "listen", code, 6, 10*time.Second)
		})
	}
}
