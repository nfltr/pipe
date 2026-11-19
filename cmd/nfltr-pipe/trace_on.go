// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

//go:build pipetrace

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Trace mode (go test -tags pipetrace, PIPETRACE=<dir>) writes one JSON
// line per step of spec/Pipe.tla's end A that run takes, one file per
// session end: <dir>/<random>.ndjson, its first line
// {"ev":"Init","na":..,"nb":..}. spec/TracePipe.tla checks each (make
// spec-traces). The two directions are goroutines of one end; the mutex
// orders their lines as they happened. One Send and one Recv stand for
// all the bytes of their direction (the model's one unit). After the end's
// last step (a failure, or Closed) nothing more is recorded: a Read after
// this end's own Reset reads as an end.
type tracer struct {
	mu               sync.Mutex
	lines            []map[string]any
	sent, got, ended bool
}

func newTracer() *tracer {
	if os.Getenv("PIPETRACE") == "" {
		return nil
	}
	return &tracer{}
}

func (t *tracer) ev(e string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.ended:
		return
	case e == "Send" && t.sent, e == "Recv" && t.got:
		return
	case e == "Send":
		t.sent = true
	case e == "Recv":
		t.got = true
	case e == "LocalFail", e == "RecvRst", e == "Closed":
		t.ended = true
	}
	t.lines = append(t.lines, map[string]any{"ev": e})
}

func (t *tracer) write() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	b2i := map[bool]int{false: 0, true: 1}
	var id [6]byte
	_, _ = rand.Read(id[:])
	f, err := os.Create(filepath.Join(os.Getenv("PIPETRACE"), hex.EncodeToString(id[:])+".ndjson"))
	if err != nil {
		panic(err)
	}
	defer f.Close() //nolint:errcheck
	enc := json.NewEncoder(f)
	for _, l := range append([]map[string]any{{"ev": "Init", "na": b2i[t.sent], "nb": b2i[t.got]}}, t.lines...) {
		if err := enc.Encode(l); err != nil {
			panic(err)
		}
	}
}
