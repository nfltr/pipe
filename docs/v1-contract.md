# nfltr-pipe v1 contract

What every 1.x release keeps. A change to anything below is a 2.0;
adding a command, a flag, a variable or an exit code is a 1.x minor
release, and a 1.x end never gives an existing one another meaning.
`TestContractDoc` (`cmd/nfltr-pipe/contract_test.go`, in `make check`)
fails when the flags, the environment variables or the exit codes below
differ from the code's, or the exit table from the usage text's and the
relay SDK's.

## Commands

| command | what |
|---|---|
| `nfltr-pipe listen [flags]` | mints a fresh address, prints it on stderr, serves one session under it: its stdin to the dialler's stdout, the dialler's stdin to its stdout; exits when that session ends |
| `nfltr-pipe dial [flags] <address>` | opens the session the address names and pipes stdin and stdout over it the same way |
| `nfltr-pipe dial [flags] --address-file <file\|->` | the same, the address read from a file only its owner may read, or (`-`) from stdin's first line, the rest of stdin the session's |
| `nfltr-pipe help`, `-h`, `--help` | the usage text on stdout, exit 0 |
| `nfltr-pipe listen -h`, `nfltr-pipe dial -h` | the usage text and the command's flags on stderr, exit 0 |
| `nfltr-pipe version`, `--version` | one line on stdout, exit 0: `nfltr-pipe <version> <commit> <go>`, the module version the go command stamped from the release's tag (a pseudo-version for a build from a checkout), the commit (`unknown` when built from the module proxy) and the Go version that built it |

Each direction ends on its own: at an end's stdin EOF the other end's
stdout reads EOF, and the other direction goes on until its own stdin
ends. One session per address: a second dial while it runs is reset (6),
a dial after it finds nothing (3).

The address on dial's command line is visible to other users of the
machine (`ps`); on a shared machine pass it with `--address-file`.

## Flags

| flag | commands | default | meaning |
|---|---|---|---|
| `--relay <address>` | listen | `$NFLTR_PIPE_RELAY`, else the relay `nfltr config set-relay` saved, else `grpc.nfltr.xyz:443` | the relay's agent-link address the listener registers at |
| `--relay-http <address>` | listen | the saved relay's `--proxy-url` host, else `--relay` without its `grpc.` prefix; port 443 dropped | the relay HTTP host the address names |
| `--cert-sha256 <sha256>` | listen, dial | `$NFLTR_RELAY_CERT_SHA256`, else the saved relay's pin | accept the relay's certificate by its SHA-256 fingerprint (a private relay's); on dial it verifies the address's relay |
| `--direct on\|off` | listen, dial | `on` | `on`: a direct path (QUIC) when the networks allow one, LAN first; `off`: relayed only (refused on listen with no machine certificate); any other value is a usage error |
| `--machine-credential <file>` | listen | `machine.json` beside the nfltr config file | this machine's certificate (`machine.key` beside it, readable by its owner only); with none found at the default the address is served with no account: direct paths only |
| `--address-file <file\|->` | dial | none | read the address from this file (refused when others may read it, except on Windows), or `-`: stdin's first line, instead of the argument |

Flags are Go `flag` syntax: `-flag value`, `--flag value`, `--flag=value`,
before or after the address (as nfltr's commands take them); every word
after the first `--` is an argument.
dial takes no relay flag: `--relay` or `--relay-http` is a usage error
(exit 1), since the address names its relay. It ignores
`$NFLTR_PIPE_RELAY`: the address names its relay; the variable applies to
listen only.

## Environment

| variable | read by | meaning |
|---|---|---|
| `$NFLTR_PIPE_RELAY` | listen | `--relay`'s default; dial ignores it (the address names its relay; the variable applies to listen only) |
| `$NFLTR_RELAY_CERT_SHA256` | listen, dial | `--cert-sha256`'s default (the relay SDK's name, shared with nfltr) |
| `$NFLTR_CONFIG` | listen, dial | the nfltr config file (default `~/.config/nfltr/nfltr.json`): its saved relay (`server`, `proxy_url`, `relay_cert_sha256`; nothing of an account), and the directory of `machine.json` |

## Exit codes

One table for nfltr-send and nfltr-pipe: the relay SDK's (`nfnet.Code`),
named as the SDK names them, so a number means the same in both and in
`nfltr cat pipe`.

| exit | code | meaning |
|---|---|---|
| 0 | | done |
| 1 | `usage` | bad flags, address or configuration, before any connection |
| 2 | `auth` | the relay refused this machine's certificate, or serves no address without an account (a relay account state) |
| 3 | `not_found` | nothing serves at the address |
| 4 | `refused` | the other end or the relay refused |
| 5 | `unreachable` | the relay or the other end was not reached in time |
| 6 | `reset` | the session was cut |
| 7 | `e2ee_pin_mismatch` | the other end is not the one the address names |
| 9 | `balance_exhausted` | the serving account's balance is exhausted (a relay account state) |
| 10 | `e2ee_client_refused` | the other end refused this end's proof: the address's secret is not its |
| 12 | `direct_required` | no direct path to an end serving with no account, or that path was lost |

8 and 11 are not used (the relay SDK leaves them to other programs).
nfltr-send's sender adds 130 and 143 for a stop; nfltr-pipe has no such
exits (a stopped end is 6).

When nfltr-pipe exits with each:

- 0: both directions ended and the other end took every byte this end
  sent.
- 1: a flag (dial given a relay, `--direct` neither on nor off), the
  address, the address file, the machine certificate, `--direct off` with
  no certificate on listen.
- 2: listen's registration; a relay that serves no address without an
  account.
- 3: dial: the listener ended, never ran, or already served its session.
- 4: dial: the relay's limits, the listener's refusal.
- 5: the relay down or wrong, no answer within the open bound.
- 6: the other end failed, was stopped or vanished; this end's stdin or
  stdout failed; this end was stopped (SIGINT, SIGTERM, SIGHUP); a second
  dial of an address in use.
- 7: dial: the key the server presents is not the address's.
- 9: listen or dial, on a relay that bills.
- 10: dial: the address's secret is not the listener's (the address was
  replaced).
- 12: dial, or either end of such a session.

## Address

`nf1` followed by base32 (RFC 4648 alphabet, lowercase, no padding) of:
version (1) | length of R | R | rid (16 bytes) | pin (32) | secret (32) |
CRC-32C of the bytes before it. R is the relay's HTTP host (no port when
443); rid the rendezvous id bound to the listener's key; pin the
listener's TLS key keyed by the secret; the secret 32 random bytes
(`github.com/nfltr/relay/client/capaddr`). It contains no `/` or `@`.
listen prints it on stderr on a line of its own that starts with `nf1`
(the only such line); the address is a secret: whoever holds it can
connect. A 1.x dial reads every address a 1.x listener prints.

## Compatibility

A 1.x end works with any 1.x end, both ways: a listener of one 1.x
release and a dialler of another open the session, carry every byte both
directions, half-close and end with the exit codes above, relayed and
direct, and with a listener with no account. `make cross-version`
(`bench/rel/cross.sh`, a check workflow job; on the nfltr CI runners
`make ci-request REPO=pipe REF=<sha> TARGET=cross-version`) runs the
previous release (`PIPE_PREV`, downloaded from its GitHub release, its
signature and sum verified) against this checkout both ways on a private
relay. Both ends need a relay that serves nf1 addresses (nfltr-relay
`NF1_ENABLED`; `NF1_ANON_ENABLED` for listeners with no account).

Release files: `nfltr-pipe_<os>_<arch>` (`.exe` on Windows) for linux,
darwin and windows on amd64 and arm64, `SHA256SUMS`, and
`SHA256SUMS.sshsig` (namespace `nfltr-release`, keys in
`release/allowed_signers`).

## Not covered

- Text on stderr: messages, their wording and order (the address line's
  shape above is covered), and the usage text's layout.
- Timings: the open bounds (30 s relayed, 60 s with direct paths), the
  liveness and detach bounds, how soon a cut is noticed.
- Which path a session takes, throughput, memory.
- Builds with `-tags pipetrace` (`$PIPETRACE`) and the relay SDK's own
  diagnostics variables.
- The Go package (`package main` has no API).
