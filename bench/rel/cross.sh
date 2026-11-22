#!/bin/sh
# Copyright 2026 nfltr
# SPDX-License-Identifier: Apache-2.0
#
# cross.sh: the released nfltr-pipe $PIPE_PREV (GitHub release, signature
# and sum verified: fetch.sh) against nfltr-pipe built from this checkout,
# both ways (the old one listens and the new one dials, then the reverse),
# relayed and direct, on rel.py's private relay: the cells CROSS_CELLS
# (default below), REL_N runs each (default 1). docs/v1-contract.md
# "Compatibility". Results in REL_OUT/<listener>-<dialler>. Exit 0 only
# when every session of both ways passed. v0.1.7 is a hard cut on the
# direct path (quiclink v0.8.0 refuses a QUIC peer without ACK Frequency):
# against an earlier PIPE_PREV a direct cell is carried relayed, and
# anon-direct (CROSS_ANON_CELLS, a run of its own) must exit 12 at the
# dialler (direct_required) both ways. The release after v0.1.7 is a hard
# cut too (relay client's nf1 direct paths seal each byte once, hint d2:
# nfltr docs/decisions/direct-once-2026-11-22.md): against v0.1.7 a direct
# cell is carried relayed, and in anon-direct this dialler exits 12 at
# once (the listener's hint is the older d1), while the older dialler
# exits 6 (its reset, naming the listener's reason: no direct path formed
# and no account).
set -eu
here=$(cd "$(dirname "$0")/../.." && pwd)
: "${PIPE_PREV:?the previous release, e.g. v0.1.2}" "${NFLTR_VERSION:?}"
out=${REL_OUT:-${TMPDIR:-/tmp}/nfltr-pipe-cross-$(date -u +%Y%m%dT%H%M%SZ)}
bin=$out/bin
mkdir -p "$bin"
(cd "$here" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$bin/nfltr-pipe" ./cmd/nfltr-pipe)
docker build -q -t nfltr-pipe-rel "$here/bench/rel" >/dev/null
cp "$here/bench/rel/fetch.sh" "$here/release/allowed_signers" "$out/"
docker run --rm -v "$out:/out" -e PIPE_VERSION="$PIPE_PREV" -e PIPE_AS=nfltr-pipe-prev -e NFLTR_VERSION="$NFLTR_VERSION" \
	nfltr-pipe-rel sh /out/fetch.sh /out/bin
echo "cross-version: nfltr-pipe $PIPE_PREV and $(cd "$here" && git describe --tags --always --dirty 2>/dev/null || echo this checkout)"
# Every byte both ways at once, a half close each way, a stop of each end
# (both exit 6), each relayed and direct; and a listener with no account.
cells=${CROSS_CELLS:-'^(size-16m-both|halfclose-listener-first|halfclose-dialler-first|term-listener-50pct|term-dialler-50pct)-(direct|relayed)$'}
anon=${CROSS_ANON_CELLS:-'^anon-direct$'}
case $PIPE_PREV in v0.1.[0-6]) anon_new=12 anon_old=12 ;; v0.1.7) anon_new=12 anon_old=6 ;; *) anon_new= anon_old= ;; esac
rc=0
for pair in "nfltr-pipe-prev nfltr-pipe $anon_new" "nfltr-pipe nfltr-pipe-prev $anon_old"; do
	set -- $pair
	anon_d=${3:-}
	echo "== listener $1, dialler $2"
	REL_BIN=$bin REL_OUT=$out/$1-$2 REL_PIPE_L=$1 REL_PIPE_D=$2 REL_N=${REL_N:-1} REL_CELLS=$cells \
		sh "$here/bench/rel/run.sh" || rc=1
	echo "== listener $1, dialler $2, $anon${anon_d:+, dialler exit $anon_d}"
	REL_EXPECT_D=$anon_d REL_BIN=$bin REL_OUT=$out/$1-$2-anon REL_PIPE_L=$1 REL_PIPE_D=$2 REL_N=${REL_N:-1} REL_CELLS=$anon \
		sh "$here/bench/rel/run.sh" || rc=1
done
exit $rc
