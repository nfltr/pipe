# Reliability matrix

`make reliability` runs nfltr-pipe through a fixed set of stream sizes,
network conditions and faults on one Linux machine with docker, and judges
every session by both ends' exit codes and by what each end's stdout
received against what the other end's stdin held: every byte, in order,
exactly once. Results: [`docs/reliability.md`](../../docs/reliability.md).

`run.sh` starts `rel.py` in a privileged container (it needs network
namespaces, netem and iptables). Inside it a private nfltr relay
(`nfltr-relay serve`, self-signed, nf1 addresses on, addresses without an
account allowed) listens on a bridge; the listener and the dialler each
have a network namespace on that bridge. netem on the two bridge ports
adds half the round trip in each direction and applies the rate cap and
the loss to what reaches each end, so the direct path and the relayed path
cross the same impairment. Offloads are off on every port, so netem sees
wire-sized packets. The listener holds a machine certificate from the
relay (enrolled with `nfltr config set-relay`), so the relay can carry its
sessions; `relayed` cells run both ends with `--direct off`. iptables
counters in each namespace count the bytes sent on the direct path (UDP
between the ends) and to the relay; a session is `direct` when over 90 %
went direct, `relayed` under 10 %, else `mixed` (it moved).

Each end's stdin is a random stream of its own (the two differ, so a byte
that went the wrong way is caught); each end's stdout goes to a file, or
through a reader the cell controls.

## Cells

| cells | what |
|---|---|
| `size-<n>-<dir>-<path>` | 0 B, 1 B, 64 KiB + 7 B and 16 MiB, from the listener (`l2d`), from the dialler (`d2l`) or both at once, no impairment |
| `large-both-<path>` | 1 GiB each way at once (`REL_LARGE`) |
| `net-<rate>-<rtt>-loss<l>-<path>` | rates 10, 100, 1000 Mbit/s x round trip 1, 40, 150 ms x loss 0, 1 %, both directions at once: 4, 16, 64 MiB each way by rate without loss, 4 MiB with loss |
| `kill-<victim>-<at>pct-<path>` | 64 MiB from the listener at 100 Mbit/s and 40 ms; once the dialler holds 25, 50 or 75 %, SIGKILL the listener or the dialler. The survivor must exit 6, within 10 s relayed and 45 s direct (the QUIC idle timeout is about 30 s) |
| `term-<victim>-50pct-<path>` | the same with SIGTERM at 50 %: both exit 6 within 10 s |
| `halfclose-<first>-first-<path>` | one end's stdin is empty; the other end starts sending its 16 MiB only after its stdout read that end. Both exit 0 |
| `reader-closes-<path>` | the dialler's stdout is closed after 1 MiB of 64 MiB (`\| head`): both exit 6 within 10 s |
| `slow-reader-<path>`, `slow-reader-128m-<path>` | 32 MiB read at 4 MiB/s, 128 MiB at 8 MiB/s: both exit 0, and the peak memory does not follow the stream |
| `drop-<s>s-<path>` | 64 MiB at 100 Mbit/s and 40 ms; at 30 % the link drops (100 % loss both ways): for 3 s both exit 0; for 10 s (about the relayed path's liveness bound) both exit 0 or both 6; for 60 s both exit 6 within 120 s |
| `relay-restart-<path>` | at 30 % the relay is killed and started again: relayed, both exit 6 within 60 s; on the direct path (the fault waits until the session runs there) the session does not need the relay and completes, both 0 |
| `one-session-<path>` | while the session runs a second dial of the address must exit 6 and the first session complete; a dial after it must exit 3 |
| `anon-direct` | a listener without a certificate (no account): direct only, 16 MiB each way |
| `anon-no-udp` | the same with UDP between the ends dropped: the dial must exit 12 (`direct_required`) |
| `anon-kill-listener-50pct` | SIGKILL the account-less listener at 50 %: the dial must exit 12 within 45 s |

A session passes when each end's stdout is the other end's stream (or, on
a cell that cuts the session, a prefix of it), each end exits with the
cell's code within its bound, and neither end's peak resident memory
(`VmHWM`) passed `REL_RSS_MB` (default 160 MiB: an end on the direct path peaks at about 100 to 130 MiB whatever the stream's size, a relayed one at about 30). Failure reasons, per end
(`l_`, `d_`): `corrupt` (a byte differs from the stream at the same offset,
or more bytes than were sent: duplicated, reordered, wrong or extra),
`truncated` (exit 0 with less than the whole stream: a cut stream taken for
a whole one), `exit_<n>`, `timeout` (`REL_TIMEOUT`, default 300 s),
`slow` (exit after the bound), `rss`; and `second_exit_<n>`,
`third_exit_<n>`. A run with any `corrupt` or `truncated` counts as
`corrupted`, which must be 0. Each cell runs `REL_N` times (default 3).

## Output

In `REL_OUT` (default `$TMPDIR/nfltr-pipe-rel-<time>`):

- `runs.jsonl`: one line per session: cell, bytes each way, fault, both
  exit codes, seconds (the dialler's start to the last exit), Mbit/s (the
  bytes of both directions over that), the bytes on each path and the path
  taken, each end's peak memory, the bytes each end received, the seconds
  from the fault to each end's exit, ok and the failure reasons.
- `cells.jsonl`: one line per cell: runs, passes, median seconds and Mbit/s
  of the passing runs, median exit after the fault, peak memory, paths
  taken, failures by reason.
- `matrix-summary.json`: totals, failures by reason, `corrupted`, the
  failed cells, the machine (kernel, CPUs, load, `net.core.rmem_max`) and
  the binaries; `matrix-summary.md`, the per-cell table.
- `logs/<cell>/<run>/`: both ends' stderr of every failed run (a passing
  run's are removed). `relay.log`.

## Running it

```console
$ make reliability                              # nfltr-pipe from this checkout, nfltr v2.1.73
$ make reliability NFLTR_VERSION=v2.1.74
$ make reliability REL_BIN=/path/to/bins        # nfltr-pipe, nfltr, nfltr-relay
$ make reliability REL_N=1 REL_CELLS='^kill-'   # a subset
```

On the nfltr CI runners: `make ci-request REPO=pipe REF=<sha>
TARGET=reliability`. It exits 0 only when every session passed.

## Cross-version

`make cross-version` (`cross.sh`, a check workflow job; on the nfltr CI
runners `make ci-request REPO=pipe REF=<sha> TARGET=cross-version`)
runs the previous release, `PIPE_PREV` in the Makefile (downloaded from its
GitHub release by `fetch.sh`, `SHA256SUMS.sshsig` verified against
`release/allowed_signers` and the binary against `SHA256SUMS`), against
nfltr-pipe built from the checkout, on this harness's private relay, twice:
the old one listens and the new one dials (`REL_PIPE_L`, `REL_PIPE_D`), then the reverse. Cells (`CROSS_CELLS`): `size-16m-both`, both half closes, a SIGTERM of each end, each relayed and direct, and `anon-direct`; `REL_N` runs each (default 1).
Results in `REL_OUT/<first>-<second>/`. It exits 0 only when every run of
both ways passed. A release moves `PIPE_PREV` to itself.

```console
$ make cross-version
$ make cross-version PIPE_PREV=v0.1.0 REL_N=3
```

Limits: one machine, so the round trip and the loss are netem's, not a
real path's, and every cell shares the machine's CPU with whatever else
runs there (each run records the load average). The host's
`net.core.rmem_max` (not namespaced) applies to the direct path's UDP
buffers; the summary records it. netem drops packets at random, not in
bursts.
