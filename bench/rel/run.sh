#!/bin/sh
# Copyright 2026 nfltr
# SPDX-License-Identifier: Apache-2.0
#
# run.sh: rel.py's matrix in a privileged container (network namespaces,
# netem, iptables), on a Linux machine with docker. Binaries: the directory
# REL_BIN (nfltr-pipe, nfltr, nfltr-relay), else nfltr-pipe built from this
# checkout and nfltr and nfltr-relay $NFLTR_VERSION (fetch.sh, verified).
# Results in REL_OUT (default $TMPDIR/nfltr-pipe-rel-<UTC time>); rel.py's
# REL_* settings pass through. Exit 0 only when every session passed.
# Containers mount only REL_OUT and REL_BIN: on a CI runner whose docker is
# the host's, only paths under TMPDIR are the same on both.
set -eu
here=$(cd "$(dirname "$0")/../.." && pwd)
out=${REL_OUT:-${TMPDIR:-/tmp}/nfltr-pipe-rel-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$out"
img=nfltr-pipe-rel
docker build -q -t $img "$here/bench/rel" >/dev/null
cp "$here/bench/rel/rel.py" "$here/bench/rel/fetch.sh" "$here/release/allowed_signers" "$out/"
if [ -z "${REL_BIN:-}" ]; then
	REL_BIN=$out/bin
	mkdir -p "$REL_BIN"
	(cd "$here" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$REL_BIN/nfltr-pipe" ./cmd/nfltr-pipe)
	docker run --rm -v "$out:/out" -e NFLTR_VERSION="${NFLTR_VERSION:?}" $img sh /out/fetch.sh /out/bin
fi
echo "results: $out"
set -- -e REL_UID="$(id -u):$(id -g)"
for v in REL_N REL_CELLS REL_TIMEOUT REL_LARGE REL_RSS_MB REL_PIPE_L REL_PIPE_D; do
	eval "x=\${$v:-}"
	[ -z "$x" ] || set -- "$@" -e "$v=$x"
done
exec docker run --rm --privileged --name "nfltr-pipe-rel-$$" "$@" \
	-v "$(cd "$REL_BIN" && pwd):/bin-in:ro" -v "$out:/out" $img python3 -u /out/rel.py matrix
