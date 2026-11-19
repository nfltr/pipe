#!/bin/sh
# Copyright 2026 nfltr
# SPDX-License-Identifier: Apache-2.0
#
# check.sh: TLC on Pipe.tla (spec/README.md): the model with -coverage 1
# (exit 1 on an action no state took; the mutants' branches excepted),
# each witness violated, each mutant caught. java and tla2tools.jar from
# $TLA_JAVA and $TLA_JAR (make spec fetches them: scripts/fetch-tla.sh).
set -eu
cd "$(dirname "$0")"
java=${TLA_JAVA:-java} jar=${TLA_JAR:?set TLA_JAR to tla2tools.jar (1.8.0)}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tlc() { cfg=$1; shift; "$java" -XX:+UseParallelGC -cp "$jar" tlc2.TLC -workers 2 -cleanup -noGenerateSpecTE -metadir "$tmp/$cfg" "$@" -config "$cfg.cfg" Pipe.tla >"$tmp/$cfg.out" 2>&1 || true; }
fail=0
tlc Pipe -coverage 1
# an action no state took (lines only a mutant takes are inside actions)
zero=$(grep -E '^<[A-Za-z]+ line .* of module Pipe>: [0-9]+:0$' "$tmp/Pipe.out" || true)
if grep -q 'No error has been found' "$tmp/Pipe.out" && [ -z "$zero" ]; then echo "ok   Pipe: invariants and Terminates hold, every action taken"
else echo "FAIL Pipe"; cat "$tmp/Pipe.out"; fail=1; fi
for c in WitnessBothClean WitnessKilledTyped WitnessHalfClose WitnessResetAfterData; do
	tlc "$c"
	if grep -q "Invariant $c is violated" "$tmp/$c.out"; then echo "ok   $c violated (reachable)"; else echo "FAIL $c not violated"; fail=1; fi
done
for m in end-on-fail rst-as-eof exit-at-eof no-close-wait close-error-ignored no-liveness; do
	tlc "mutant-$m"
	if v=$(grep -E -o 'Invariant [A-Za-z]+ is violated|Temporal properties were violated|Deadlock reached' "$tmp/mutant-$m.out"); then echo "ok   mutant $m caught ($v)"; else echo "FAIL mutant $m not caught"; fail=1; fi
done
exit $fail
