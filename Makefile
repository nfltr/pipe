# Copyright 2026 nfltr
# SPDX-License-Identifier: Apache-2.0

# make check: gofmt, vet (here and for windows/amd64), the windows/amd64
# build, -race tests; make spec: the TLA+ model's invariants, witnesses
# and mutants (spec/README.md); make spec-traces: the tests with -tags
# pipetrace, each session end's trace checked against the model with TLC,
# and the mutant traces rejected (spec/README.md "Traces"). All run in CI,
# and the tests on Windows too.
.PHONY: ci check spec spec-traces release release-sign release-verify release-dryrun reliability cross-version
# make ci: the check workflow's Linux jobs but cross-version, for a CI runner
# of our own.
ci: check spec spec-traces release-dryrun

check:
	./scripts/check-dep-ports.sh self-test
	./scripts/check-dep-ports.sh
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go vet -tags pipetrace ./...
	GOOS=windows GOARCH=amd64 go vet ./...
	GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/nfltr-pipe
	go test -race ./...

spec:
	@set -e; t=$$(sh scripts/fetch-tla.sh); \
	TLA_JAVA=$$(echo "$$t" | sed -n 1p) TLA_JAR=$$(echo "$$t" | sed -n 2p) sh spec/check.sh

spec-traces:
	@set -e; t=$$(sh scripts/fetch-tla.sh); \
	TLA_JAVA=$$(echo "$$t" | sed -n 1p) TLA_JAR=$$(echo "$$t" | sed -n 2p) sh spec/traces.sh

# Release files in dist/ (scripts/release.sh): make release builds the six
# binaries and SHA256SUMS, reproducibly; make release-sign signs SHA256SUMS
# (SHA256SUMS.sshsig) with pipe's key, ~/.config/nfltr-pipe/release-signing-key
# or SIGNING_KEY=<key>; make release-verify checks both
# against release/allowed_signers; make release-dryrun builds twice,
# compares, and signs and verifies with a throwaway key. Tags and GitHub
# releases are made by hand from a verified dist/.
release:
	sh scripts/release.sh build
release-sign:
	sh scripts/release.sh sign
release-verify:
	sh scripts/release.sh verify
release-dryrun:
	sh scripts/release.sh dryrun

# make reliability: the reliability matrix (bench/rel/README.md; results in
# docs/reliability.md) on a Linux machine with docker: every cell REL_N
# times (default 3), nfltr-pipe built from this checkout, nfltr and
# nfltr-relay $(NFLTR_VERSION) from the installer, or REL_BIN=<dir> with all
# three. Exit 0 only when every session passed.
NFLTR_VERSION ?= v2.1.78
export NFLTR_VERSION
reliability:
	sh bench/rel/run.sh

# make cross-version: the previous release PIPE_PREV (downloaded from its
# GitHub release, signature and sums verified) against nfltr-pipe built
# from this checkout, both ways, relayed and direct, on a private relay
# (bench/rel/cross.sh; docs/v1-contract.md "Compatibility"); Linux with
# docker, about 3 minutes. A check workflow job; not in make ci. A release
# moves PIPE_PREV to itself.
PIPE_PREV ?= v0.1.7
export PIPE_PREV
cross-version:
	sh bench/rel/cross.sh
