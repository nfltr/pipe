# Changelog

## v0.1.8

- **A direct session uses less CPU.** On a direct path each byte is now
  encrypted once, by the direct QUIC connection's TLS 1.3 (it already
  checks the other end's pinned key and is bound to the session), instead
  of a second time by the session's end-to-end TLS inside it; a relayed
  path keeps the session's TLS, also when a session moves between relayed
  and direct (relay client v0.25.0). nfltr-send's 10 GiB direct transfer,
  on the same change between two 8-vCPU cloud machines in one zone, took
  6 % less CPU per GiB on the sender and 6-8 % less on the receiver at an
  unchanged rate; nfltr-pipe was not measured.
- **Hard cut on the direct path: both ends need v0.1.8 for direct.**
  Between v0.1.8 and v0.1.7 or earlier no direct path forms: a listener
  with an account carries the session relayed (every byte, as before);
  to a listener with no account a v0.1.8 dialler exits 12
  (`direct_required`) at once, and a v0.1.7 dialler exits 6 (a protocol
  error: its reset names the listener's reason, no direct path and no
  account). The relayed path is unchanged.
- **Fewer acknowledgements on paths that reorder deeply.** v0.1.7's ACK
  Frequency did not reduce acknowledgements on a real long path (measured
  with nfltr-send, Helsinki to Singapore, 260 ms): on a path that reorders
  packets by more than 10 places, the receiving end still acknowledged
  each such late packet at once. Now, once the sending end sees that
  reordering, it asks again for no immediate acknowledgement of reordered
  packets (quiclink v0.9.0, its QUIC fork v0.63.1-nfltr.9). This aims to
  cut acknowledgements and their CPU on such paths; it is not measured on
  a real path yet.
- Built on link v0.13.3, pathswitch v0.1.4, quiclink v0.9.0, relay client
  v0.25.0, relay proto v0.22.2 and relay-core v0.19.6.

## v0.1.7

- **The direct path asks for fewer acknowledgements** (QUIC's ACK
  Frequency extension, quiclink v0.8.0 on its QUIC fork v0.63.1-nfltr.7):
  the receiving end acknowledges every 10 packets and only a packet more
  than 10 places late at once, instead of an immediate ACK for every
  reordered packet. On a simulated path that reorders 30 % of its packets,
  0.39 -> 0.10 ACK packets per data packet at the same rate (the QUIC
  library's test); nfltr-pipe was not measured, and the CPU this saves on
  a real reordering path is not measured yet.
  Measured on nfltr-send v0.1.12 after the release, on a real 260 ms
  path (Helsinki to Singapore): it did not reduce acknowledgements; the
  cause is being worked on.
- **Hard cut on the direct path: both ends need v0.1.7.** A QUIC peer
  without ACK Frequency is refused, so between v0.1.7 and v0.1.6 or
  earlier no direct path forms: a listener with an account carries the
  session relayed (every byte, as before), and a dial of a listener with
  no account exits 12 (`direct_required`). The relayed path is unchanged.
- **A listener whose move to another relay replica failed moves when the
  relay asks again**; before, it ignored the second ask and stayed where
  the relay was rebalancing away from (relay client v0.24.5).
- Built on link v0.13.3, pathswitch v0.1.4, quiclink v0.8.0, relay client
  v0.24.5, relay proto v0.22.1 and relay-core v0.19.5.

## v0.1.6

- **The direct path's transport of nfltr-send v0.1.11**, which made its
  direct transfers faster on fast links for less CPU. The path switch's
  send and receive buffers are rings, so an acknowledgement no longer
  moves every unacknowledged byte to the front of its buffer (pathswitch
  v0.1.4); on
  Linux the direct path's QUIC reads datagrams in batches (UDP GRO), a
  send batch holds up to 44 packets (was 14), and a connection queues up
  to 4096 received packets (quiclink v0.7.4). Between two machines in
  one cloud zone nfltr-send's 10 GiB direct transfer ran at 2.71 Gbit/s
  on it, 1.54 before (nfltr-pipe was not measured).
- **A closed session no longer leaves a write hanging**: a direct
  session's write blocked on the peer's window returns an error once the
  session is closed; before, it waited (quiclink v0.7.4).
- The wire is unchanged from v0.1.5.
- Built on link v0.13.3, pathswitch v0.1.4, quiclink v0.7.4, relay client
  v0.24.4, relay proto v0.22.1 and relay-core v0.19.4.

## v0.1.5

- **A listener whose machine was off past its certificate's life serves
  again**: the expired certificate is renewed before the first connection
  presents it (before: the relay refused it for good and `listen` exited
  6). A refused machine's error is one line: no certificate dump, no relay
  answer, `nfltr config login` named once; it no longer says `--relay
  names another`.
- **A machine certificate renews through a pinned private relay**: the
  renewal trusts the relay certificate that `--cert-sha256` (else
  `$NFLTR_RELAY_CERT_SHA256`) pins, as the sessions do; before, it used
  the system roots only, a self-signed relay refused every renewal and
  the certificate lapsed.
- **Direct sessions on long, lossy paths**: the direct path's QUIC uses
  BBR instead of NewReno (link quiclink v0.7.2). On a direct path
  Helsinki to Singapore (260 ms, 0.03-0.06 % loss, 29 % of packets
  reordered) a 10 GiB transfer ran at a steady 352-359 Mbit/s; before,
  36-166 Mbit/s, falling to 15-59 (nfltr-send, the same transport). The
  wire is unchanged.
- **Hole punching**: the dialling end sends its first QUIC packet half a
  relay round trip after the sync, once the other end's first probe has
  opened its NAT (before: at the sync, and the first packet met a closed
  NAT); an attempt that no candidate answered is followed by another, at
  most three per round, before the session stays relayed; a round for a
  session that has ended stops (relay client v0.24.1).
- Built on link v0.13.3, relay client v0.24.1, relay proto v0.22.1 and
  relay-core v0.19.1.

From the v1.0 surface review ([docs/v1-surface-review.md](docs/v1-surface-review.md)).

- **Flags may follow the address**: `nfltr-pipe dial nf1... --direct off`
  now dials relayed only (before: exit 1, "name the address"). After
  `--` every word is an argument.
- A test keeps built binaries out of the repository.
- **`--relay-cert-sha256` is now `--cert-sha256`**, the name `nfltr config
  set-relay` and nfltr's `cat` commands use (one name in all three tools);
  `--relay-cert-sha256` is refused. `$NFLTR_RELAY_CERT_SHA256` is
  unchanged.

## v0.1.4

- **The machine certificate renews itself after an outage.** A listener
  that keeps listening renews its certificate at half its lifetime, up to
  10 % later (before: at two thirds), and while the relay cannot be
  reached it keeps retrying, also past the certificate's expiry: a relay
  renews an expired certificate within its grace (90 days), so the listener
  needs no new `nfltr config login` once its relay answers again. Before,
  renewal stopped at the expiry. Only a final refusal (the machine
  revoked, or expired past the grace) stops it, and the listener logs that
  (relay client v0.23.11).
- **Built on link v0.13.0, the API link 1.0 keeps.** The wire is
  unchanged: a v0.1.4 end works with a v0.1.3 end, both ways, relayed and
  direct (`make cross-version` now checks it against v0.1.3).
  A relay built on it also ends two joined sessions whose ends cross
  cleanly; before, both ends could be reset 30 s after reading every byte.
- **Sessions ride the relay's agent link, or its gRPC streams**: the
  relay client no longer opens separate data connections to a relay that
  offers them (relay client v0.23.12).
- Built on link v0.13.0, relay client v0.23.12, relay proto v0.21.5 and
  relay-core v0.17.0.

## v0.1.3

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
- **Relayed, an end exits 0 only when its peer has the whole stream.** A
  relayed close returned once the relay had the last bytes, so when the
  peer's link dropped right then an end could exit 0 while its peer exited
  6 with a prefix. A relayed close now waits for the peer to acknowledge
  the end through the relay, as a direct close does, and gives up when the
  peer stays silent past its bound: that end then exits 6 (link v0.12.20,
  relay client v0.23.10, relay-core v0.16.9;
  [defect 4](docs/reliability.md#defects-found)).
- **The contract of a 1.x release:** [docs/v1-contract.md](docs/v1-contract.md)
  lists the commands, flags, environment variables, exit codes, the
  address format and the compatibility rule; a test fails when the code
  and it differ.
- **`make cross-version`:** the previous release, downloaded from GitHub
  and verified, against this checkout, both ways, relayed and direct, on
  a private relay; a check workflow job.
- The README names the pin flag's variable right: `$NFLTR_RELAY_CERT_SHA256`
  (it said `$NFLTR_RELAY_PIN`, which nothing reads).
- Built on link v0.12.20, relay client v0.23.10, relay proto v0.21.4 and
  relay-core v0.16.9.

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
