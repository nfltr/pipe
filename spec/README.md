# spec: nfltr-pipe's end of a pipe (TLA+)

`Pipe.tla` models the two ends of one pipe (`docs/design.md`): each copies
its stdin into the session and the session into its stdout, over a
session with a half close (END), a failed end (RST) and a bounded credit
(`Cap` messages in flight per direction). An end half-closes at its
stdin's end, closes its stdout at the other side's END, and once both
directions ended it closes the session, which returns once the other side
took every byte and the END (`Finish`, `Closed`); only then does it exit 0.
A failure of its stdin or stdout, or a signal, resets the session and
exits 6 (`LocalFail`); a cut session (the other end's reset, or the SDK's
liveness cutting a vanished peer's) exits 6 too (`RecvRst`). A killed end
sends nothing more (`Kill`); the environment then cuts its peer's session
(`Liveness`). 6 stands for any typed non-zero code: on the direct path of
an address served with no account the SDK reads a vanished peer as the
path lost, 12.

`make spec` (TLC 1.8.0, fetched by `scripts/fetch-tla.sh`; or
`spec/check.sh` with `TLA_JAVA`, `TLA_JAR`) runs the model with
`-coverage 1`, each witness (must be violated) and each mutant (must be
caught). NA = NB = 2 bytes, Cap = 2, one fault: 909 states, under a second.

| invariant / property | meaning |
|---|---|
| `CleanExit` | an end that exited 0 wrote every byte of the other's stdin to its stdout, and the other side took every byte of its stdin and its END |
| `NoFalseEOF` | an end reads the other's END only after all its bytes: a failed or stopped end never reads as a finished one |
| `Terminates` | every end that is not killed exits (weak fairness on each end's steps and on the SDK's liveness) |

| witness (violated) | shows |
|---|---|
| `WitnessBothClean` | both ends exit 0 |
| `WitnessKilledTyped` | one end killed, the other exits typed |
| `WitnessHalfClose` | an end reads the other's END and still sends |
| `WitnessResetAfterData` | an end exits 6 after bytes arrived |

| mutant | the failure it plants | caught by |
|---|---|---|
| `end-on-fail` | a failed end sends END, not RST (relay client: the agent ended an nf1 session's TLS with close_notify at its target's reset, fixed in `TestNF1TargetResetIsResetAtClient`) | `NoFalseEOF` |
| `rst-as-eof` | a reset read as the other side's END | `NoFalseEOF` |
| `exit-at-eof` | exit 0 at the other side's END while stdin still goes | `CleanExit` |
| `no-close-wait` | exit 0 without the other side taking every byte (relay client: a clean Close dropped the data connection with its frames still buffered, fixed in `TestNF1CloseKeepsWhatWasWritten`) | `CleanExit` |
| `close-error-ignored` | a session cut during the Close taken as a clean end | `CleanExit` |
| `no-liveness` | a vanished peer never cuts the session | deadlock (`Terminates`) |

## Traces

`make spec-traces` runs the tests with `-tags pipetrace`: each session end
(`run` in `cmd/nfltr-pipe/main.go`) writes its steps as NDJSON
(`trace_on.go`: one file per end, its first line whether its stdin had
bytes and whether it read any, one `Send` and one `Recv` standing for the
bytes of each direction). `TracePipe.tla` checks each against `Pipe.tla`
with the recording end as A and the other end and the environment hidden;
a trace is accepted when a behaviour takes every line. The script lists
an action no accepted trace takes (a test gap) and requires every fixture
in `traces/` named `mutant-*` to be rejected: `exit-at-eof`,
`eof-before-data`, `exit-0-after-reset`, `unknown-step` (a logged step
that maps to no action).

## Environment assumptions

| assumption | source |
|---|---|
| a session's bytes arrive in order, once, and its END after them; a reset is read as an error, never as an END | relay client `nfnet.Conn` (its Read: "io.EOF only at the peer's half close"), link `WIRE.md` |
| a clean `Close` returns once the session's frames are written to the relay or the direct path | relay client `nfnet` `dropMux` (Drain) and the dialer's and listener's Close |
| a vanished peer's session is cut within the SDK's liveness bounds | link ping liveness (`Liveness` presets), the direct path's QUIC idle timeout |
| a process's local stdin and stdout fail with an error, not a hang (SIGPIPE ignored) | `cmd/nfltr-pipe/main.go` |

## Code map

| model | code |
|---|---|
| Send, CloseWrite, Recv, RecvEnd, LocalFail, RecvRst, Finish, Closed | cmd/nfltr-pipe/main.go |
| the recorder | cmd/nfltr-pipe/trace_on.go |
