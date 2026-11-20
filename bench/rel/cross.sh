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
# when every session of both ways passed.
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
cells=${CROSS_CELLS:-'^((size-16m-both|halfclose-listener-first|halfclose-dialler-first|term-listener-50pct|term-dialler-50pct)-(direct|relayed)|anon-direct)$'}
rc=0
for pair in "nfltr-pipe-prev nfltr-pipe" "nfltr-pipe nfltr-pipe-prev"; do
	set -- $pair
	echo "== listener $1, dialler $2"
	REL_BIN=$bin REL_OUT=$out/$1-$2 REL_PIPE_L=$1 REL_PIPE_D=$2 REL_N=${REL_N:-1} REL_CELLS=$cells \
		sh "$here/bench/rel/run.sh" || rc=1
done
exit $rc
