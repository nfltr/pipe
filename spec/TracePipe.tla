------------------------------ MODULE TracePipe ------------------------------
\* Copyright 2026 nfltr
\* SPDX-License-Identifier: Apache-2.0
\*
\* A recorded end of a pipe (go test -tags pipetrace, cmd/nfltr-pipe
\* trace_on.go) checked against Pipe.tla: the recording end is A, the
\* other end and the environment are hidden (any of their steps may come
\* between two lines). Line 1 is {"ev":"Init","na":..,"nb":..}: whether
\* A's stdin had bytes, and whether A read any; the recorder logs one
\* Send and one Recv for all of them (a unit of data). Accepted when a
\* behaviour takes every line (TraceNotDone violated); a line that maps to
\* no action of A's, or takes one out of order, is rejected.
EXTENDS Naturals, Sequences, Json

CONSTANT TraceFile

Log == ndJsonDeserialize(TraceFile)

VARIABLES in, up, down, got, ch, pc, code, cut, faults, l

P == INSTANCE Pipe WITH NA <- Log[1].na, NB <- Log[1].nb, Cap <- 2, MaxFaults <- 1, Mutant <- "none"

pvars == <<in, up, down, got, ch, pc, code, cut, faults>>

TraceInit == P!Init /\ l = 2

\* The other end's and the environment's steps: hidden.
Hidden ==
    /\ \/ P!Step("B") \/ P!LocalFail("B") \/ P!Kill("B")
       \/ P!Liveness("A") \/ P!Liveness("B")
    /\ UNCHANGED l


TSend == l <= Len(Log) /\ Log[l].ev = "Send" /\ P!Send("A") /\ l' = l + 1
TCloseWrite == l <= Len(Log) /\ Log[l].ev = "CloseWrite" /\ P!CloseWrite("A") /\ l' = l + 1
TRecv == l <= Len(Log) /\ Log[l].ev = "Recv" /\ P!Recv("A") /\ l' = l + 1
TRecvEnd == l <= Len(Log) /\ Log[l].ev = "RecvEnd" /\ P!RecvEnd("A") /\ l' = l + 1
TRecvRst == l <= Len(Log) /\ Log[l].ev = "RecvRst" /\ P!RecvRst("A") /\ l' = l + 1
TLocalFail == l <= Len(Log) /\ Log[l].ev = "LocalFail" /\ P!LocalFail("A") /\ l' = l + 1
TFinish == l <= Len(Log) /\ Log[l].ev = "Finish" /\ P!Finish("A") /\ l' = l + 1
TClosed == l <= Len(Log) /\ Log[l].ev = "Closed" /\ P!Closed("A") /\ l' = l + 1

TraceNext == Hidden \/ TSend \/ TCloseWrite \/ TRecv \/ TRecvEnd \/ TRecvRst \/ TLocalFail \/ TFinish \/ TClosed

TraceSpec == TraceInit /\ [][TraceNext]_<<pvars, l>>

\* Violated once a behaviour took every line: the trace is accepted.
TraceNotDone == l <= Len(Log)
=============================================================================
