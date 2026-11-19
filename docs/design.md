# Design: the end of a pipe

nfltr-pipe joins one end's stdin to the other's stdout, both ways, over
one relay SDK session (`github.com/nfltr/relay/client/nfnet`: an nf1
address, end to end TLS, direct or relayed). This note says who closes
what and when, what happens when a peer stops or vanishes, and what bounds
the memory. The checked model is `spec/Pipe.tla` (`spec/README.md`).

## Who closes what, and when

Each end runs two copies and nothing else (`run`, `up`, `down` in
`cmd/nfltr-pipe/main.go`):

- **up**: stdin to the session. At stdin's end it half-closes the session
  (`CloseWrite`): the other side reads the end of its stdout after every
  byte, and its own direction goes on.
- **down**: the session to stdout. At the other side's half close it
  closes stdout, so a reader (`tar x`) sees its end there.
- When **both** ended, the end closes the session (`Close`): its frames
  are written before the data connection closes (the SDK drains it), and
  the end exits 0. Exit 0 means: this end wrote every byte the other sent,
  and handed every byte of its own and the half close to the transport.
- **Anything else resets** the session (`Reset`) and exits non-zero: a
  failed read of stdin, a failed write of stdout (`| head` closing early:
  SIGPIPE is ignored so this is a write error, not death), SIGINT or
  SIGTERM, or a session the SDK reports cut. The other side reads a reset,
  never an end, so it never takes a cut stream for a whole one; stdout is
  left open on a failure (a Read after this end's own Reset reads as an
  end and must not close it).

A listener serves one session per address: a second dial is reset. The
listener closes once its session ended.

## When a peer stops or vanishes

- **Stopped or failed** (signal, stdin or stdout error): it resets; this
  end's next read or write fails typed and it exits 6.
- **Killed or gone** (kill -9, crash, the machine or its network gone): no
  reset is sent. Relayed, the relay sees its connection end and cuts the
  session at once (the tests measure milliseconds); on a direct path the
  SDK's liveness (QUIC idle timeout) cuts it within about 30 s; with no
  account behind the address that reads as the direct path lost, exit 12.
- A dialler also watches the session's end (`Conn.Done`): one that ends
  while its stdin is still going is a reset, even with no byte to write.
  An accepted session has no such signal (its `Done` is its own Close), so
  a listener whose stdout already ended and whose stdin is idle learns of
  a vanished dialler at its next write.

## Buffer bounds

No buffer of nfltr-pipe's own beyond one 32 KiB copy buffer per direction
(`io.Copy`). A write blocks on the session's credit (the SDK buffers
nothing beyond it), so a slow reader at one end slows the writer at the
other; nothing grows with the stream.

## Failure model and tests

| invariant | test that fails if it breaks |
|---|---|
| every byte, both ways, exactly, and exit 0 at both ends; a side with an empty stdin does not lose its peer's tail | `TestRoundTrip` (8 MiB/3 MiB, 16 MiB/0, 0/16 MiB, 1/1 bytes; direct and relayed) |
| a half close keeps the other direction going | `TestHalfClose` |
| a failed end resets: the other exits 6 and its stdout is not closed | `TestLocalFailureResets`, `TestStoppedEndResets` |
| a killed peer ends the other end typed, within the liveness bound | `TestKilledPeerIsTyped` (each end killed, each path) |
| open failures are typed (12, 1, 3) | `TestOpenFailuresAreTyped` |
| the code takes only the model's steps, in the model's order | `make spec-traces` |

Two defects of the relay client found here were fixed there, test first
(relay `land/NFCAT`): a clean Close of an nf1 session cut its still
buffered frames (`TestNF1CloseKeepsWhatWasWritten`), and an nf1 session's
reset reached the other end as a clean end (`TestNF1TargetResetIsResetAtClient`).
Each is a mutant of `spec/Pipe.tla`.

## Prior art

Half close and the reset/clean-end distinction are TCP's (RFC 9293
section 3.6: FIN and RST); end to end TLS tells a truncation from an end by
close_notify (RFC 8446 section 6.1). The session, its credit and the
direct paths are the relay SDK's; nfltr-pipe adds no protocol and no
framing: the bytes on the session are exactly the bytes of stdin.
