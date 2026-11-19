// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

//go:build !pipetrace

package main

// The trace recorder (trace_on.go) is compiled only with -tags pipetrace.
type tracer struct{}

func newTracer() *tracer  { return nil }
func (*tracer) ev(string) {}
func (*tracer) write()    {}
