# Changelog

## v0.1.2 (unreleased)

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
