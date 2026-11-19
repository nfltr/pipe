#!/bin/sh
# Copyright 2026 nfltr
# SPDX-License-Identifier: Apache-2.0
#
# traces.sh: the cmd/nfltr-pipe tests under -tags pipetrace write each
# session end's steps (trace_on.go); each trace is checked against
# Pipe.tla with TracePipe.tla (accepted when a behaviour takes every
# line), the actions no accepted trace takes are listed (test gaps), and
# every fixture in spec/traces/ named mutant-* must be rejected
# (spec/README.md "Traces"). java and tla2tools.jar from $TLA_JAVA and
# $TLA_JAR (make spec-traces fetches them).
set -eu
cd "$(dirname "$0")"
java=${TLA_JAVA:-java} jar=${TLA_JAR:?set TLA_JAR to tla2tools.jar (1.8.0)}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/t"
(cd .. && PIPETRACE=$tmp/t go test -tags pipetrace -count=1 ./cmd/nfltr-pipe/) >"$tmp/test.out" 2>&1 || { cat "$tmp/test.out"; exit 1; }

# check FILE: TLC on FILE; FILE.out. Accepted: TraceNotDone violated.
check() {
	d=$tmp/w/$(basename "$1")
	mkdir -p "$d"
	sed "1s|^|CONSTANT TraceFile = \"$1\"\n|" TracePipe.cfg >"$d/t.cfg"
	"$java" -XX:+UseSerialGC -cp "$jar" tlc2.TLC -workers 1 -cleanup -noGenerateSpecTE -metadir "$d" -config "$d/t.cfg" TracePipe.tla >"$1.out" 2>&1 || true
	grep -q 'Invariant TraceNotDone is violated' "$1.out"
}

fail=0 n=0
for f in "$tmp"/t/*.ndjson; do
	n=$((n + 1))
	(check "$f" && touch "$f.ok") &
	[ $((n % ${TRACE_JOBS:-4})) -ne 0 ] || wait
done
wait
for f in "$tmp"/t/*.ndjson; do
	if [ -f "$f.ok" ]; then cat "$f.out" >>"$tmp/cov"; else
		echo "REJECTED $(basename "$f")"; cat "$f"; grep -E '^Error' "$f.out" | tail -3
		fail=1
	fi
done
echo "$n traces checked"
[ "$n" -gt 0 ] || { echo "FAIL no trace written"; fail=1; }
for a in TSend TCloseWrite TRecv TRecvEnd TRecvRst TLocalFail TFinish TClosed; do
	grep -qE "^State [0-9]+: <$a line .* of module TracePipe>" "$tmp/cov" || echo "test gap: no trace takes $a"
done
# A fixture is rejected when TLC finished without a behaviour taking every
# line, never by an error of TLC's own.
for f in traces/mutant-*.ndjson; do
	if ! check "$PWD/$f" && grep -q 'No error has been found' "$f.out"; then echo "ok   $f rejected"
	else echo "FAIL $f accepted or not checked"; cat "$f.out"; fail=1; fi
	rm -f "$f.out"
done
exit $fail
