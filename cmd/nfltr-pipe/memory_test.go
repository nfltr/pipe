// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"io"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nfltr/relay/client/nfnet"
)

// counted is a stdin of n zero bytes that counts what was read of it.
type counted struct{ n, read atomic.Int64 }

func (c *counted) Read(p []byte) (int, error) {
	left := c.n.Load() - c.read.Load()
	if left <= 0 {
		return 0, io.EOF
	}
	k := min(int64(len(p)), left)
	clear(p[:k])
	c.read.Add(k)
	return int(k), nil
}

// gated is a stdout whose writes wait for open to close.
type gated struct {
	open chan struct{}
	got  atomic.Int64
}

func (g *gated) Write(p []byte) (int, error) {
	<-g.open
	g.got.Add(int64(len(p)))
	return len(p), nil
}

func (g *gated) Close() error { return nil }

// TestMemoryBoundedPerDirection backs "memory does not grow with the
// stream: one 32 KiB buffer per direction, and a write waits for the
// session's credit" (README "Operating notes", docs/design.md "Buffer
// bounds"): with the receiver's stdout stalled, the sender reads no more
// of a 128 MiB stdin than the session's credit and the copy buffers hold,
// the heap does not grow with it, and every byte arrives once the stdout
// takes again.
func TestMemoryBoundedPerDirection(t *testing.T) {
	// Measured 2026-11-26: relayed 7 MiB read, heap +10 MiB; direct (QUIC
	// windows) 24 MiB read, heap +49 MiB. Neither depends on total.
	const total, readBound, heapBound = 128 << 20, 32 << 20, 64 << 20
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			r := testRelay(t)
			in := new(counted)
			in.n.Store(total)
			addr, lc := startListen(t, t.Context(), r, p, in, newOut())
			tg, err := nfnet.ParseTarget(addr)
			if err != nil {
				t.Fatal(err)
			}
			var base runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&base)
			g := &gated{open: make(chan struct{})}
			dc := make(chan int, 1)
			go func() { dc <- dial(t.Context(), dialCfg(r), tg, p.direct, stdio{bytes.NewReader(nil), g, os.Stderr}) }()
			// A sender that buffers its stdin reads past readBound within this
			// window; one held by credit stops below it.
			for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
				if n := in.read.Load(); n > readBound {
					t.Fatalf("read %d MiB of stdin with the receiver's stdout stalled, want at most %d MiB", n>>20, readBound>>20)
				}
			}
			var now runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&now)
			if grew := int64(now.HeapAlloc) - int64(base.HeapAlloc); grew > heapBound {
				t.Fatalf("heap grew %d MiB with the receiver's stdout stalled, want at most %d MiB", grew>>20, heapBound>>20)
			}
			t.Logf("stalled: read %d KiB of stdin, heap %+d KiB", in.read.Load()>>10, (int64(now.HeapAlloc)-int64(base.HeapAlloc))>>10)
			close(g.open)
			wait(t, "dial", dc, 0)
			wait(t, "listen", lc, 0)
			if got := g.got.Load(); got != total {
				t.Fatalf("dial's stdout got %d bytes, want %d", got, total)
			}
		})
	}
}
