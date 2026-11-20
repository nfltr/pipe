# Changelog

## Unreleased

These change what scripts see; v1.0 keeps them.

- **`dial` takes no relay flag.** `--relay` and `--relay-http` were
  ignored by dial (the address names its relay); now each is a usage
  error (exit 1) that says so. `$NFLTR_PIPE_RELAY` stays listen's only:
  dial ignores it.
- **`--relay-pin` is now `--relay-cert-sha256`**, the name of its
  variable `$NFLTR_RELAY_CERT_SHA256` and of nfltr's `config set-relay
  --cert-sha256`.
- **`--direct` takes `on` or `off` only**; `true`, `false` and an empty
  value are usage errors.
- **`listen -h` and `dial -h` exit 0**, as `help` does.
- **`nfltr-pipe version`** (and `--version`) prints the release, its
  commit and the Go version.
- **`dial --address-file -`** reads the address from stdin's first line
  and pipes the rest, so the address stays off the command line, which
  other users of a shared machine can see.
- The exit table is now nfltr-send's too: one table, the relay SDK's.

- **A second dial of an address in use exits 6, not 10.** The second
  dial could read the listener's reset as a refused secret (exit 10,
  `e2ee_client_refused`); it now exits 6 (`reset`), as the exit table says
  (relay client, nf1; [defect 3](docs/reliability.md#defects-found)).
- **The contract of a 1.x release:** [docs/v1-contract.md](docs/v1-contract.md)
  lists the commands, flags, environment variables, exit codes, the
  address format and the compatibility rule; a test fails when the code
  and it differ.
- **`make cross-version`:** the previous release, downloaded from GitHub
  and verified, against this checkout, both ways, relayed and direct, on
  a private relay; a check workflow job.
- The README names the pin flag's variable right: `$NFLTR_RELAY_CERT_SHA256`
  (it said `$NFLTR_RELAY_PIN`, which nothing reads).

## v0.1.2

- **A direct session under loss no longer loses its tail.** On a slow,
  lossy direct path an end could exit 0 while the other end exited 6 with
  a right but short stream: bytes its transport had already taken were
  dropped by the reset at the sender's exit. They are now kept and read
  to the end (link v0.12.18; [defect 1](docs/reliability.md#defects-found)).
- **A stopped end resets its peer before it exits.** When one end stops
  early (its stdout closed, Ctrl-C) on a direct session, the other end now
  exits 6 within about a second; before, it waited about 30 s for a lost
  path (relay client v0.23.8; defect 2).
- **Exit 0 only when the peer has the whole stream.** A direct session's
  close now waits as long as the peer keeps acknowledging (before, it
  reset the session after 30 s even while the tail was still arriving),
  gives up 5 s after the peer goes silent, and exits 6 when the peer never
  acknowledged the end, where it could exit 0 before (relay client
  v0.23.8).
- Built on link v0.12.18, relay client v0.23.8, relay proto v0.21.2 and
  relay-core v0.16.7.

## v0.1.1

- **The relay you chose is the one used.** nfltr-pipe takes its relay from
  `--relay`, else `$NFLTR_PIPE_RELAY`, else the relay `nfltr config
  set-relay` saved, else `grpc.nfltr.xyz:443`. Reading the saved relay
  reads only its address and pin, never an account or key. Before, it
  always dialled nfltr.xyz.
- **Connect errors name the relay.** A listen that cannot reach its relay
  says which relay it tried and that `--relay` names another (`relay
  <address> (--relay names another): ...`); a machine certificate made for
  another relay says so and names `--relay`.
- **A reliability matrix** (`make reliability`, [docs/reliability.md](docs/reliability.md)):
  stream sizes, rates, round trips and loss, killed and stopped ends, half
  closes, slow readers, link drops and a relay restart, on the direct and
  the relayed path, every byte checked in order and exactly once. Its
  first run: every byte that arrived was right.
- **A listener whose direct-path peer was killed stops promptly**, and a
  session that arrives while it is stopping is refused rather than left
  open (relay client v0.23.7).
- **A cut stream is never taken for a whole one** (link v0.12.17): a
  session this side resets ends its reads with an error, not a clean end.
- The install snippet installs into `~/.local/bin` and creates it first.
- Built on link v0.12.17, relay client v0.23.7, relay proto v0.21.1 and
  relay-core v0.16.6.
