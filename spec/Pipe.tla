-------------------------------- MODULE Pipe --------------------------------
\* Copyright 2026 nfltr
\* SPDX-License-Identifier: Apache-2.0
\*
\* The end of a pipe (spec/README.md): two ends, each copying its stdin
\* into one session and the session into its stdout, over a session with
\* a half close (END), a failed end (RST) and a bounded credit (Cap
\* messages in flight per direction). An end exits 0 only after both
\* directions ended cleanly and the peer acknowledged every byte it sent;
\* any other end resets the session and exits 6. A killed end sends
\* nothing; the environment (the relay SDK's liveness: the agent link's
\* ping, the direct path's QUIC idle timeout) cuts its peer's session.
EXTENDS Naturals, Sequences

CONSTANTS NA, NB,    \* bytes on A's and B's stdin
          Cap,       \* messages in flight per direction (the session's credit)
          MaxFaults, \* local failures and kills, in all
          Mutant     \* "none", or the planted failure (README "Mutants")

Ends == {"A", "B"}
Other(e) == IF e = "A" THEN "B" ELSE "A"
N(e) == IF e = "A" THEN NA ELSE NB

VARIABLES in,     \* bytes still on e's stdin
          up,     \* e's sending direction: "open", "ended" (END sent)
          down,   \* e's receiving direction: "open", "eof"
          got,    \* bytes e wrote to its stdout
          ch,     \* messages from e in flight: "d", "end", "rst"
          pc,     \* "run", "closing" (Close: waits for the acknowledgement), "exited", "dead"
          code,   \* e's exit status once exited; 99 before
          cut,    \* the environment cut the session of e's dead peer
          faults

vars == <<in, up, down, got, ch, pc, code, cut, faults>>

Init ==
    /\ in = [e \in Ends |-> N(e)]
    /\ up = [e \in Ends |-> "open"]
    /\ down = [e \in Ends |-> "open"]
    /\ got = [e \in Ends |-> 0]
    /\ ch = [e \in Ends |-> <<>>]
    /\ pc = [e \in Ends |-> "run"]
    /\ code = [e \in Ends |-> 99]
    /\ cut = [e \in Ends |-> FALSE]
    /\ faults = 0

Live(e) == pc[e] \in {"run", "closing"}
Incoming(e) == ch[Other(e)]

\* e's copy of stdin into the session: a byte, within the credit.
Send(e) ==
    /\ pc[e] = "run" /\ up[e] = "open" /\ in[e] > 0 /\ Len(ch[e]) < Cap
    /\ in' = [in EXCEPT ![e] = @ - 1]
    /\ ch' = [ch EXCEPT ![e] = Append(@, "d")]
    /\ UNCHANGED <<up, down, got, pc, code, cut, faults>>

\* stdin's EOF: CloseWrite, the half close.
CloseWrite(e) ==
    /\ pc[e] = "run" /\ up[e] = "open" /\ in[e] = 0 /\ Len(ch[e]) < Cap
    /\ up' = [up EXCEPT ![e] = "ended"]
    /\ ch' = [ch EXCEPT ![e] = Append(@, "end")]
    /\ UNCHANGED <<in, down, got, pc, code, cut, faults>>

\* A peer's byte, written to stdout.
Recv(e) ==
    /\ pc[e] = "run" /\ down[e] = "open"
    /\ Incoming(e) # <<>> /\ Head(Incoming(e)) = "d"
    /\ got' = [got EXCEPT ![e] = @ + 1]
    /\ ch' = [ch EXCEPT ![Other(e)] = Tail(@)]
    /\ UNCHANGED <<in, up, down, pc, code, cut, faults>>

\* The peer's half close: EOF, stdout closed.
RecvEnd(e) ==
    /\ pc[e] = "run" /\ down[e] = "open"
    /\ Incoming(e) # <<>> /\ Head(Incoming(e)) = "end"
    /\ down' = [down EXCEPT ![e] = "eof"]
    /\ ch' = [ch EXCEPT ![Other(e)] = Tail(@)]
    /\ UNCHANGED <<in, up, got, pc, code, cut, faults>>

\* Reset: the session ends as failed; what e had in flight is lost and
\* the peer reads RST (never a clean end).
Reset(e) == ch' = [ch EXCEPT ![e] = IF Mutant = "end-on-fail" THEN Append(@, "end") ELSE <<"rst">>]

\* A local failure (stdin's read or stdout's write failed) or a signal:
\* e resets the session and exits 6.
LocalFail(e) ==
    /\ Live(e) /\ faults < MaxFaults
    /\ Reset(e)
    /\ pc' = [pc EXCEPT ![e] = "exited"]
    /\ code' = [code EXCEPT ![e] = 6]
    /\ faults' = faults + 1
    /\ UNCHANGED <<in, up, down, got, cut>>

\* The session cut (the peer reset it, or the SDK cut a vanished peer's):
\* a Read, a Write or the Close fails; e exits 6.
RecvRst(e) ==
    /\ Live(e)
    /\ \/ Incoming(e) # <<>> /\ Head(Incoming(e)) = "rst"
       \/ cut[e]
    /\ IF Mutant = "rst-as-eof" /\ pc[e] = "run" /\ down[e] = "open"
       THEN /\ down' = [down EXCEPT ![e] = "eof"]
            /\ UNCHANGED <<pc, code>>
       ELSE /\ pc' = [pc EXCEPT ![e] = "exited"]
            /\ code' = [code EXCEPT ![e] = IF Mutant = "close-error-ignored" /\ pc[e] = "closing" THEN 0 ELSE 6]
            /\ UNCHANGED down
    /\ ch' = [ch EXCEPT ![Other(e)] = IF @ # <<>> /\ Head(@) = "rst" THEN Tail(@) ELSE @]
    /\ cut' = [cut EXCEPT ![e] = FALSE]
    /\ UNCHANGED <<in, up, got, faults>>

\* Both directions ended: Close, which returns once the peer
\* acknowledged every byte and the END.
Finish(e) ==
    /\ pc[e] = "run" /\ down[e] = "eof"
    /\ up[e] = "ended" \/ Mutant = "exit-at-eof"
    /\ IF Mutant \in {"no-close-wait", "exit-at-eof"}
       THEN /\ pc' = [pc EXCEPT ![e] = "exited"]
            /\ code' = [code EXCEPT ![e] = 0]
       ELSE /\ pc' = [pc EXCEPT ![e] = "closing"]
            /\ UNCHANGED code
    /\ UNCHANGED <<in, up, down, got, ch, cut, faults>>

Closed(e) ==
    /\ pc[e] = "closing" /\ ch[e] = <<>> /\ ~cut[e]
    /\ pc' = [pc EXCEPT ![e] = "exited"]
    /\ code' = [code EXCEPT ![e] = 0]
    /\ UNCHANGED <<in, up, down, got, ch, cut, faults>>

\* e killed (kill -9, a crash, the machine gone): it sends nothing more.
Kill(e) ==
    /\ Live(e) /\ faults < MaxFaults
    /\ pc' = [pc EXCEPT ![e] = "dead"]
    /\ faults' = faults + 1
    /\ UNCHANGED <<in, up, down, got, ch, code, cut>>

\* The SDK's liveness cuts the session of a dead end's peer.
Liveness(e) ==
    /\ pc[Other(e)] = "dead" /\ Live(e) /\ ~cut[e]
    /\ Mutant # "no-liveness"
    /\ cut' = [cut EXCEPT ![e] = TRUE]
    /\ UNCHANGED <<in, up, down, got, ch, pc, code, faults>>

Step(e) == Send(e) \/ CloseWrite(e) \/ Recv(e) \/ RecvEnd(e) \/ RecvRst(e) \/ Finish(e) \/ Closed(e)

Next ==
    \/ \E e \in Ends : Step(e) \/ LocalFail(e) \/ Kill(e) \/ Liveness(e)
    \/ (\A e \in Ends : pc[e] \in {"exited", "dead"}) /\ UNCHANGED vars

Spec == Init /\ [][Next]_vars /\ \A e \in Ends : WF_vars(Step(e)) /\ WF_vars(Liveness(e))

TypeOK ==
    /\ \A e \in Ends : in[e] \in 0..N(e) /\ got[e] \in 0..N(Other(e))
    /\ up \in [Ends -> {"open", "ended"}] /\ down \in [Ends -> {"open", "eof"}]
    /\ pc \in [Ends -> {"run", "closing", "exited", "dead"}]
    /\ code \in [Ends -> {0, 6, 99}] /\ cut \in [Ends -> BOOLEAN]
    /\ faults \in 0..MaxFaults

\* An end that exited 0 wrote every byte of its peer's stdin to its
\* stdout, and its peer's session took every byte of its stdin and its END.
CleanExit == \A e \in Ends : code[e] = 0 => got[e] = N(Other(e)) /\ in[e] = 0 /\ up[e] = "ended" /\ ch[e] = <<>>

\* EOF only after every byte of the peer's stdin: a failed or stopped end
\* never reads as a clean one.
NoFalseEOF == \A e \in Ends : down[e] = "eof" => got[e] = N(Other(e))

\* Every end that is not killed exits.
Terminates == <>(\A e \in Ends : pc[e] \in {"exited", "dead"})

\* Witnesses (README): each must be violated.
WitnessBothClean == ~(\A e \in Ends : code[e] = 0)
WitnessKilledTyped == ~(\E e \in Ends : pc[e] = "dead" /\ code[Other(e)] = 6)
WitnessHalfClose == ~(\E e \in Ends : down[e] = "eof" /\ up[e] = "open" /\ in[e] < N(e))
WitnessResetAfterData == ~(\E e \in Ends : code[e] = 6 /\ got[e] > 0 /\ pc[Other(e)] = "exited")
=============================================================================
