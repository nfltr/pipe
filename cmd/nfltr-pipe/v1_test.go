// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

// dial refuses a relay (the address names its relay) as a usage error,
// never ignores it; --direct takes on or off only; -h exits 0.
func TestDialRefusesARelayAndDirectIsOnOrOff(t *testing.T) {
	const addr = "nf1aaaa"
	for name, c := range map[string]struct {
		env  string
		args []string
		say  string
	}{
		"--relay":            {"", []string{"dial", "--relay", "x:443", addr}, "takes no --relay"},
		"--relay=":           {"", []string{"dial", "--relay=x:443", addr}, "takes no --relay"},
		"--relay-http":       {"", []string{"dial", "--relay-http", "x", addr}, "takes no --relay-http"},
		"$NFLTR_PIPE_RELAY":  {"x:443", []string{"dial", addr}, "$NFLTR_PIPE_RELAY is set"},
		"--direct true":      {"", []string{"dial", "--direct", "true", addr}, `--direct "true": want on or off`},
		"listen --direct=":   {"", []string{"listen", "--direct="}, `--direct "": want on or off`},
		"--relay-pin (gone)": {"", []string{"dial", "--relay-pin", "00", addr}, "not defined: -relay-pin"},
	} {
		t.Setenv("NFLTR_PIPE_RELAY", c.env)
		var code int
		msg := stderrOf(t, func() { code = cli(c.args) })
		if code != 1 || !strings.Contains(msg, c.say) || strings.Count(msg, "\n") != 1 {
			t.Errorf("%s: exit %d, %q: want 1 saying %q on one line", name, code, msg, c.say)
		}
	}
	for _, cmd := range []string{"listen", "dial"} {
		var code int
		stderrOf(t, func() { code = cli([]string{cmd, "-h"}) })
		if code != 0 {
			t.Errorf("%s -h exited %d, want 0", cmd, code)
		}
	}
}

// --address-file - reads the address from stdin's first line and leaves
// the rest of stdin to the session.
func TestAddressFromStdinLeavesTheRest(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = io.WriteString(w, " nf1abc \npayload")
		_ = w.Close()
	}()
	stdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = stdin }()
	a, err := readAddress("-")
	rest, _ := io.ReadAll(r)
	if err != nil || a != "nf1abc" || string(rest) != "payload" {
		t.Fatalf("address %q (%v), rest %q: want nf1abc, payload", a, err, rest)
	}
}

func TestVersion(t *testing.T) {
	if v := versionLine(); !strings.HasPrefix(v, "nfltr-pipe ") || !strings.HasSuffix(v, " "+runtime.Version()) || len(strings.Fields(v)) != 4 {
		t.Fatalf("version %q: want nfltr-pipe <version> <commit> <go version>", v)
	}
}
