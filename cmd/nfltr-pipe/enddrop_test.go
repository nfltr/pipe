// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nfltr/relay/client/nfnet"
)

// eofCut is a stdin that calls cut when it reaches its end.
type eofCut struct {
	io.Reader
	cut func()
}

func (e eofCut) Read(p []byte) (int, error) {
	n, err := e.Reader.Read(p)
	if err == io.EOF {
		e.cut()
	}
	return n, err
}

// TestRelayedEndDropAfterLastByte (lane SENDONCE, send's defect 3 checked
// here): relayed, the dialler's link to the relay stops carrying (a drop,
// not a close) once the listener's stdin ended. The listener's Close
// returns once the relay has its bytes; the dialler never gets the tail
// and its liveness cuts it. An end that exits 0 promises its peer took
// every byte (bench/rel `exit0_peer_short`), so the listener must not
// exit 0 while the dialler holds a prefix.
func TestRelayedEndDropAfterLastByte(t *testing.T) {
	r := testRelay(t)
	var mu sync.Mutex
	var quiet []*atomic.Bool
	cut := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, q := range quiet {
			q.Store(true)
		}
	}
	a := random(t, 4<<20)
	la, da := newOut(), newOut()
	addr, lc := startListen(t, t.Context(), r, paths[1], eofCut{bytes.NewReader(a), cut}, la)
	tg, err := nfnet.ParseTarget(addr)
	if err != nil {
		t.Fatal(err)
	}
	cfg := nfnet.Config{Relay: "relay.test", Plaintext: true, DialRelay: func(ctx context.Context, _ string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", r.Proxy)
		if err != nil {
			return nil, err
		}
		q := new(atomic.Bool)
		mu.Lock()
		quiet = append(quiet, q)
		mu.Unlock()
		return quietConn{c, q}, nil
	}}
	dc := make(chan int, 1)
	go func() { dc <- dial(t.Context(), cfg, tg, nfnet.DirectOff, stdio{bytes.NewReader(nil), da, io.Discard}) }()
	l, d := <-lc, <-dc
	t.Logf("listen exit %d, dial exit %d, dial got %d of %d bytes", l, d, len(da.bytes()), len(a))
	if l == 0 && !bytes.Equal(da.bytes(), a) {
		t.Fatalf("listen exited 0, dial (exit %d) got %d of its %d bytes", d, len(da.bytes()), len(a))
	}
}

// quietConn drops what it reads once quiet is set, without closing: a
// link that stops carrying.
type quietConn struct {
	net.Conn
	q *atomic.Bool
}

func (c quietConn) Read(p []byte) (int, error) {
	for {
		n, err := c.Conn.Read(p)
		if err != nil || !c.q.Load() {
			return n, err
		}
	}
}

func (c quietConn) Write(p []byte) (int, error) {
	if c.q.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}
