# Changelog

## v0.1.1 (unreleased)

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
