#!/bin/sh
# Copyright 2026 nfltr
# SPDX-License-Identifier: Apache-2.0
#
# fetch.sh <dir>: nfltr and nfltr-relay $NFLTR_VERSION for linux/amd64, from
# the nfltr installer, which verifies its own signatures. nfltr enrols the
# listener's machine certificate; nfltr-relay is the private relay.
set -eu
d=$1
: "${NFLTR_VERSION:?}"
mkdir -p "$d" && cd "$d"
curl -fsSL https://nfltr.xyz/install.sh -o install.sh
NFLTR_VERSION=$NFLTR_VERSION NFLTR_INSTALL_DIR=$d sh install.sh --with relay >install.log 2>&1 || { tail -20 install.log >&2; exit 1; }
rm install.sh
for b in nfltr nfltr-relay; do test -x "$d/$b"; done
