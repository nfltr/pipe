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
| `nfltr-pipe dial [flags] --address-file <file>` | the same, the address read from a file only its owner may read |
| `nfltr-pipe help`, `-h`, `--help` | the usage text on stdout, exit 0 |

Each direction ends on its own: at an end's stdin EOF the other end's
stdout reads EOF, and the other direction goes on until its own stdin
ends. One session per address: a second dial while it runs is reset (6),
a dial after it finds nothing (3).

## Flags

| flag | commands | default | meaning |
|---|---|---|---|
| `--relay <address>` | listen, dial | `$NFLTR_PIPE_RELAY`, else the relay `nfltr config set-relay` saved, else `grpc.nfltr.xyz:443` | the relay's agent-link address the listener registers at (dial ignores it: the address names its relay) |
| `--relay-http <address>` | listen, dial | the saved relay's `--proxy-url` host, else `--relay` without its `grpc.` prefix; port 443 dropped | the relay HTTP host the address names (dial ignores it) |
| `--relay-pin <sha256>` | listen, dial | `$NFLTR_RELAY_CERT_SHA256`, else the saved relay's pin | accept the relay's certificate by its SHA-256 fingerprint (a private relay's); on dial it verifies the address's relay |
| `--direct on\|off` | listen, dial | `on` | `on`: a direct path (QUIC) when the networks allow one, LAN first; `off`: relayed only (refused on listen with no machine certificate) |
| `--machine-credential <file>` | listen | `machine.json` beside the nfltr config file | this machine's certificate (`machine.key` beside it, readable by its owner only); with none found at the default the address is served with no account: direct paths only |
| `--address-file <file>` | dial | none | read the address from this file (refused when others may read it, except on Windows) instead of the argument |

Flags are Go `flag` syntax: `-flag value`, `--flag value`, `--flag=value`.

## Environment

| variable | read by | meaning |
|---|---|---|
| `$NFLTR_PIPE_RELAY` | listen, dial | `--relay`'s default |
| `$NFLTR_RELAY_CERT_SHA256` | listen, dial | `--relay-pin`'s default (the relay SDK's name, shared with nfltr) |
| `$NFLTR_CONFIG` | listen, dial | the nfltr config file (default `~/.config/nfltr/nfltr.json`): its saved relay (`server`, `proxy_url`, `relay_cert_sha256`; nothing of an account), and the directory of `machine.json` |

## Exit codes

| exit | code | meaning | when |
|---|---|---|---|
| 0 | | both directions ended and the other end took every byte this end sent | the session's clean end |
| 1 | `usage` | bad flags, address or configuration | before any connection: a flag, the address, the address file, the machine certificate, `--direct off` with no certificate |
| 2 | `auth` | the relay refused this machine's certificate | listen's registration; a relay that serves no address without an account |
| 3 | `not_found` | nothing listens at the address | dial: the listener ended, never ran, or already served its session |
| 4 | `refused` | the listener or the relay refused the session | dial: the relay's limits, the listener's refusal |
| 5 | `unreachable` | the relay or the listener was not reached in time | the relay down or wrong, no answer within the open bound |
| 6 | `reset` | the session was cut | the other end failed, was stopped or vanished; this end's stdin or stdout failed; this end was stopped (SIGINT, SIGTERM, SIGHUP); a second dial of an address in use |
| 7 | `e2ee_pin_mismatch` | the listener is not the one the address names | dial: the key the server presents is not the address's |
| 9 | `balance_exhausted` | the serving account's balance is exhausted | listen or dial, on a relay that bills |
| 10 | `e2ee_client_refused` | the listener refused this end's proof | dial: the address's secret is not the listener's (the address was replaced) |
| 12 | `direct_required` | no direct path to a listener serving with no account, or that path was lost | dial, or either end of such a session |

8 and 11 are not used (the relay SDK leaves them to other programs). The
codes and their names are the relay SDK's (`nfnet.Code`), so scripts can
treat `nfltr-pipe`, `nfltr-send`'s typed codes and `nfltr cat pipe` alike.

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

## Before freezing: cut or rename

For the owner's decision; nothing here is changed yet.

1. **`dial --relay` and `dial --relay-http`**: accepted and ignored (the
   address names its relay); `$NFLTR_PIPE_RELAY` too. Cut them from dial
   (a usage error), so a 1.x dial never has to keep ignoring them.
2. **`--relay-pin` and `$NFLTR_RELAY_CERT_SHA256`**: the flag and its
   variable have different names (the README said `$NFLTR_RELAY_PIN`,
   which nothing reads; fixed in this lane). The variable is the SDK's
   and nfltr's; rename the flag `--relay-cert-sha256` (nfltr's
   `config set-relay --cert-sha256`), or keep both names as they are.
3. **`--direct` takes more than on|off**: the SDK also accepts `true`,
   `false` and the empty string (as off). Accept only `on` and `off`
   before 1.0, or the others are a 1.x promise.
4. **`listen -h` and `dial -h` exit 1**, `help` and `-h` alone exit 0.
   Make a command's `-h` exit 0.
5. **No `version` command**: neither end can say which release it is,
   which a cross-version report needs. Add `nfltr-pipe version` (an
   addition, possible in 1.x, but better in 1.0).
6. **The address on the command line**: `dial <address>` puts the secret
   where other users of the machine see it; nfltr-send's receive takes it
   only from a file or stdin. Keep both ways, or make the file (or
   stdin, `--address-file -`) the only one.
7. **Exit 9 (`balance_exhausted`) and 2 (`auth`)** are the hosted relay's
   account states; with free use they may never happen. Keep them (they
   cost nothing) or fold them into 4 before 1.0.
