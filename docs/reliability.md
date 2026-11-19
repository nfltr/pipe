# Reliability

Every number here comes from `make reliability`
([`bench/rel/`](../bench/rel/README.md)), which anyone can rerun on a Linux
machine with docker. A private relay, a listener and a dialler run in their
own network namespaces on one machine; netem sets the rate, the round trip
and the loss for the direct and the relayed path alike. Each session is
judged by both ends' exit codes against the table in the README and by
what each end's stdout received against what the other end's stdin held:
every byte, in order, exactly once; a prefix only when the session was
cut; the whole stream whenever an end exits 0. Any defect fails the run.

## Baseline, 2026-11-19

nfltr-pipe at `97026e1` (relay client v0.23.6) with nfltr and nfltr-relay
v2.1.73, the released binaries. One Linux machine (16 CPUs, Linux 6.12)
shared with other jobs: load average 2 to 76 during the run, median 31.
`net.core.rmem_max` was the kernel's default (212,992 bytes). 99 cells,
3 runs each, 297 sessions, 43 minutes.

- **Every byte that arrived was right**: in all 297 sessions each end's
  stdout was the other end's stream or, where the session was cut, a
  prefix of it. Corrupted (a wrong, duplicated, reordered or extra byte, or
  exit 0 with a short stream): 0.
- **The run fails**: 288 of 297 passed. 7 failed on three defects of the
  relay client and link (below, "Defects found"), all on direct-path
  sessions. The other 2 were 10 s link drops that cut a relayed session
  with both ends exiting 6: 10 s is about the relayed path's liveness
  bound, and the matrix now accepts a cut there (both ends 6, never one 0).
- Peak memory: up to 123 MiB per end on the direct path, whatever the
  stream's size (32 MiB and 128 MiB read slowly peak alike); up to 84 MiB
  relayed, 25 to 30 MiB for most relayed sessions.

### Stream sizes

No impairment. Each entry is the sessions that passed, then the median
Mbit/s (the bytes of both directions over the dialler's run, from its start
to the last exit, so the session's opening and the dialler's close count).

| size | direction | direct | relayed |
|---|---|---|---|
| 0 B | listener to dialler, dialler to listener, both | 9/9 | 9/9 |
| 1 B | each way, both | 9/9 | 9/9 |
| 64 KiB + 7 B | listener to dialler | 3/3, 1.0 | 3/3, 1.0 |
| 64 KiB + 7 B | dialler to listener | 3/3, 1.0 | 3/3, 1.0 |
| 64 KiB + 7 B | both | 3/3, 1.9 | 3/3, 2.0 |
| 16 MiB | listener to dialler | 3/3, 195.6 | 3/3, 231.1 |
| 16 MiB | dialler to listener | 3/3, 173.3 | 3/3, 198.2 |
| 16 MiB | both | 3/3, 308 | 3/3, 356.8 |
| 1 GiB | both | 3/3, 2466 | 3/3, 1768 |

A session of a few bytes takes about half a second end to end: the dial's
close waits for the other end to close its direct connections.

### Rate, round trip and loss

Both directions at once: 4, 16 or 64 MiB each way at 10, 100 or 1000
Mbit/s without loss, 4 MiB with 1 % loss in each direction. Each entry is
the sessions that passed, then the median Mbit/s of both directions.

| rate | RTT | direct | relayed | direct, 1 % loss | relayed, 1 % loss |
|---|---|---|---|---|---|
| 10 Mbit/s | 1 ms | 3/3, 16.3 | 3/3, 16.3 | 3/3, 12 | 3/3, 16.3 |
| 10 Mbit/s | 40 ms | 3/3, 12.7 | 3/3, 15.2 | 3/3, 11.4 | 3/3, 12.8 |
| 10 Mbit/s | 150 ms | 3/3, 12 | 3/3, 10.6 | 3/3, 3.6 | 3/3, 4.8 |
| 100 Mbit/s | 1 ms | 3/3, 125.2 | 3/3, 138.2 | 3/3, 63.1 | 3/3, 64.5 |
| 100 Mbit/s | 40 ms | 3/3, 90.8 | 3/3, 90.3 | 2/3, 13.7 (defect 1) | 3/3, 14.8 |
| 100 Mbit/s | 150 ms | 3/3, 79.8 | 3/3, 62.5 | 1/3, 4.6 (defect 1) | 3/3, 4.5 |
| 1000 Mbit/s | 1 ms | 3/3, 813.9 | 3/3, 846.5 | 3/3, 105.1 | 3/3, 121 |
| 1000 Mbit/s | 40 ms | 3/3, 556.3 | 3/3, 742.4 | 3/3, 13.1 | 3/3, 19.1 |
| 1000 Mbit/s | 150 ms | 3/3, 232.5 | 3/3, 324.3 | 3/3, 5.4 | 3/3, 5.1 |

These are small streams on a netem path, so slow start and loss recovery
dominate the long round trips; the numbers are measurements, not a target.
The 10 Mbit/s cells pass 10 Mbit/s each way, so both directions together
exceed the rate.

### Faults

64 MiB from the listener at 100 Mbit/s and 40 ms unless the cell says
otherwise. Each entry is the sessions that passed, then the median seconds
from the fault to the last end's exit.

| fault | direct | relayed |
|---|---|---|
| listener killed (SIGKILL) at 25, 50, 75 % | 9/9, 34 s | 9/9, 0.6 s |
| dialler killed at 25, 50 % | 6/6, 34 s | 6/6, 0.5 s |
| dialler killed at 75 % | 3/3, 21 s (the listener exits 0, see below) | 3/3, 0.5 s |
| listener stopped (SIGTERM) at 50 % | 3/3, 0.5 s | 3/3, 0.6 s |
| dialler stopped at 50 % | 3/3, 0.5 s | 3/3, 0.5 s |
| half close, listener first / dialler first (16 MiB) | 6/6 | 6/6 |
| the dialler's stdout closed after 1 MiB | 0/3, 31 s (defect 2) | 3/3, 0.9 s |
| slow reader, 32 MiB at 4 MiB/s | 3/3 | 3/3 |
| link dropped 3 s at 30 % | 3/3, 9.9 s | 3/3, 9.4 s |
| link dropped 10 s | 3/3, 17.2 s | 3/3: 1 went on, 2 cut (both ends 6) at 15.5 s |
| link dropped 60 s | 3/3, both 6 at 34 s | 3/3, both 6 at 15 s |
| relay killed and restarted at 30 % | 3/3, completed, 6 s | 3/3, both 6 at 0.5 s |
| second dial during the session; dial after it | 2/3 (defect 3) | 3/3 |
| no account: direct, 16 MiB each way | 3/3 | - |
| no account, UDP between the ends dropped: dial exits 12 | 3/3, 55 s | - |
| no account, listener killed at 50 %: dial exits 12 | 3/3, 3.9 s | - |

A killed peer ends the other end's direct session at the session's detach
bound (30 s, then the exit): about 34 s. A listener whose dialler was
killed at 75 % exited 0 in all three runs: the dialler's transport had
taken the whole stream and its END before the kill, which is what exit 0
promises ([design](design.md): every byte handed to the transport and
acknowledged); the dialler's stdout held a right prefix (48 of 64 MiB).
On a relayed session the liveness bound is about 10 to 15 s: a 10 s drop
is cut or survived, a 60 s drop is cut at about 15 s.

The slow reader at 128 MiB and 8 MiB/s (`slow-reader-128m`, added after
the baseline, run separately: 2 runs per path) passed 4/4 with the same
peak memory as at 32 MiB: 96 to 114 MiB direct, 24 to 29 MiB relayed.

## With the relay client fixes, 2026-11-19

The same matrix (99 cells, 3 runs each) with nfltr-pipe built against the
relay client's main plus `land/PIPEMATRIX` (defect 2's fix and Close's
error), the same nfltr and nfltr-relay, on the same machine (41 minutes):
293 of 297 passed, corrupted 0. The dialler whose stdout closed early now
resets the listener in 1 s (3/3, was 31 s). What remains is defect 1
(2 sessions: 10 and 1000 Mbit/s, 150 ms, 1 % loss, direct) and defect 3
(2 of 3 second dials exited 10).

## On the r24 modules, 2026-11-19 (a subset)

nfltr-pipe at `c175d56` (relay client v0.23.7, link v0.12.17; defect 2's
fix is not in v0.23.7) with nfltr and nfltr-relay v2.1.73, linux/arm64 in
docker on a Mac, not the CI machine: the cells without impairment only
(stream sizes, half closes, kills and stops at 50 %, one session, no
account), 1 run each, 41 sessions, 3.5 minutes. 40 passed, corrupted 0;
the one failure is defect 3 (`one-session-direct`, the second dial exited
10).

## Defects found

Each was found by this matrix and belongs to the relay SDK, not to
nfltr-pipe: none is worked around here.

1. **Bytes the receiver's transport acknowledged are dropped by a later
   reset** (link `pathswitch`; the relay client's agent). On a direct-path
   session under loss (100 and 1000 Mbit/s, 40 and 150 ms, 1 % loss; 3 of
   6 baseline sessions at 100 Mbit/s, 2 of 2 at 1000 Mbit/s in the first
   run) the listener exited 0 and the dialler exited 6 with a right but
   short stream (as little as 1.3 of 4 MiB): `session reset: pathswitch:
   session reset`. A pathswitch end acknowledges bytes once it holds them,
   before its reader reads them, and its Read reports a reset before the
   bytes it holds. The likely path: the listener's Close returned clean
   (every byte and its END acknowledged), and its agent's stop at its exit
   reset the session before Close's background clean end had run, an ABORT
   that link's model allows only while the direction is not clean
   (`spec/DirectPath.tla` `AbortBy`: `~Clean`; the code's `abort` has no
   such guard, and the model's receiver delivers what it acknowledges). Reproducer:
   `REL_CELLS='^net-100mbit-150ms-loss1-direct$' REL_N=10`; in-process,
   an `Abort` right after a successful `psConn.Close` loses every byte the
   peer had not yet read (the relay client's `agent` package, 0 of 262,144
   bytes read). Open.
2. **A reset on a direct-path session that is still relayed reaches the
   peer only at its detach bound** (relay client `agent`, `psConn.Reset`):
   the dialler's stdout failed after 1 MiB, it reset and exited at once,
   and the listener exited 6 only 31 s later (3 of 3). Reset queued the
   ABORT and returned; the process exited before it was written, and the
   peer read the stream's end as a lost path. Fixed in the relay client
   (`land/PIPEMATRIX`, `TestPSConnResetReachesAPeerBeforeExit`): Reset
   returns once the peer has the ABORT, at most 2 s.
3. **A second dial during the session exits 10, not 6** (relay client
   nf1): 1 of 3 direct runs read `the server refused this address's
   secret` (`e2ee_client_refused`) where the README's table and "a second
   dial of the same address is reset" say 6. Reproducer:
   `REL_CELLS='^one-session-direct$' REL_N=10`. Open.

The matrix also found that a direct session's Close returned nil when the
peer had not acknowledged the END (the session failed first): fixed in
the relay client with defect 2 (`TestPSConnCloseReportsAnUnackedEnd`);
in the baseline it hid behind defect 1.
