#!/usr/bin/env python3
# Copyright 2026 nfltr
# SPDX-License-Identifier: Apache-2.0
"""rel.py matrix: nfltr-pipe's reliability matrix, as root in one machine
(run.sh starts it in a privileged container). A relay, a listener and a
dialler in their own network namespaces on one bridge; netem on the bridge
ports caps the rate, adds the round trip and drops packets in both
directions, for the direct and the relayed path alike. Every session is
judged by both ends' exit codes and by what each end's stdout received
against what the other end's stdin held: byte for byte, in order, nothing
extra; a cut session may leave a prefix, never a wrong byte, and an end
that exits 0 must hold the whole stream. README.md lists the cells.

Env: REL_BIN (nfltr-pipe, nfltr, nfltr-relay; default /bin-in), REL_OUT
(default /out), REL_N (runs per cell, default 3), REL_CELLS (regex: the
cells to run), REL_TIMEOUT (s per session, default 300), REL_LARGE (bytes
each way of the large cells, default 1 GiB), REL_RSS_MB (an end's resident
memory bound, default 160), REL_UID (owner of REL_OUT's files at the end), REL_PIPE_L and REL_PIPE_D (the
listener's and the dialler's nfltr-pipe in REL_BIN, default nfltr-pipe: two
versions, cross.sh), REL_EXPECT_D (every cell's expected dialler exit code,
the listener then not judged by its exit: cross.sh across a hard cut)."""
import json, os, re, signal, statistics, subprocess as sp, sys, threading, time

B = os.environ.get("REL_BIN", "/bin-in")
OUT = os.environ.get("REL_OUT", "/out")
N = int(os.environ.get("REL_N", "3"))
TIMEOUT = int(os.environ.get("REL_TIMEOUT", "300"))
RSS_MB = int(os.environ.get("REL_RSS_MB", "160"))
MiB = 1 << 20
LARGE = int(os.environ.get("REL_LARGE", str(1024 * MiB)))
W = "/w"
RELAY = "10.77.0.1"
IP = {"l": "10.77.0.2", "d": "10.77.0.3"}
PIPE = {"l": os.environ.get("REL_PIPE_L", "nfltr-pipe"), "d": os.environ.get("REL_PIPE_D", "nfltr-pipe")}


def sh(cmd, check=True):
    return sp.run(cmd, shell=True, check=check, capture_output=True, text=True).stdout.strip()


# ---- the network ----

def net_up():
    sh(f"ip link add br0 type bridge && ip addr add {RELAY}/24 dev br0 && ip link set br0 up")
    for n, ip in IP.items():
        sh(f"ip netns add {n} && ip link add v{n} type veth peer name e0 netns {n} && ip link set v{n} master br0 up"
           f" && ip -n {n} addr add {ip}/24 dev e0 && ip -n {n} link set e0 up && ip -n {n} link set lo up")
        # netem must see wire-sized packets, not offloaded super-packets.
        sh(f"ip netns exec {n} ethtool -K e0 tso off gso off gro off tx-udp-segmentation off", check=False)
        sh(f"ethtool -K v{n} tso off gso off gro off tx-udp-segmentation off", check=False)
        # Byte counters, no verdict: UDP to the other end (the direct path) and anything to the relay.
        peer = IP["d" if n == "l" else "l"]
        sh(f"ip netns exec {n} iptables -A OUTPUT -p udp -d {peer} && ip netns exec {n} iptables -A OUTPUT -d {RELAY}")


def impair(rate=0, rtt=0, loss=0):
    """netem on both bridge ports (what reaches the listener and the
    dialler): half the round trip each way, the rate cap and the loss."""
    for d in ("vl", "vd"):
        if not (rate or rtt or loss):
            sh(f"tc qdisc del dev {d} root", check=False)
            continue
        lim = 100000 if not rate else max(1000, int(rate * 1e6 * (rtt / 2000 + 0.2) / 8 / 1500))
        sh(f"tc qdisc replace dev {d} root netem delay {rtt * 500}us"
           + (f" rate {rate}mbit" if rate else "") + f" loss {loss}% limit {lim}")


def block_udp(on):
    op = "-A" if on else "-D"
    sh(f"ip netns exec d iptables {op} INPUT -p udp -s {IP['l']} -j DROP", check=on)
    sh(f"ip netns exec d iptables {op} OUTPUT -p udp -d {IP['l']} -j DROP", check=on)


def counters(zero=False):
    """(direct, relay) bytes both ends sent since the last zero: UDP between
    the ends, and everything to the relay."""
    direct = relay = 0
    for n in IP:
        if zero:
            sh(f"ip netns exec {n} iptables -Z OUTPUT")
            continue
        for m in re.finditer(r"\[\d+:(\d+)\] -A OUTPUT -d (\S+)/32", sh(f"ip netns exec {n} iptables-save -c -t filter")):
            if m.group(2) == RELAY:
                relay += int(m.group(1))
            else:
                direct += int(m.group(1))
    return direct, relay


# ---- the relay and the listener's certificate ----

PIN = ""
ENV = {}
RELAYP = None


def relay_start():
    global RELAYP
    d = f"{W}/relay"
    os.makedirs(d, mode=0o700, exist_ok=True)
    env = dict(os.environ, HOME=d, NF1_ENABLED="true", NF1_HOSTS=f"{RELAY}:8444", NF1_ANON_ENABLED="true",
               RATELIMIT_ENABLED="false", SECURITY_CONN_LIMIT_PER_IP_PACKET_RATE="0")
    # One listener and one dialler address open every session: lift the per-address and per-source caps.
    for k in ("NF1_ADDRESS", "NF1_SOURCE", "NF1_ANON_SOURCE"):
        env.update({k + "_MAX": "1000", k + "_BURST": "100000", k + "_RATE": "100000"})
    env.update(NF1_OPEN_BURST="100000", NF1_OPEN_RATE="100000")
    log = open(f"{OUT}/relay.log", "ab")
    RELAYP = sp.Popen([f"{B}/nfltr-relay", "serve", "--data-dir", d, "--tls-self-signed", "--tls-name", RELAY,
                       "--listen", RELAY, "--agent-rate-limit", "0"], env=env, stdout=log, stderr=log, start_new_session=True)
    for _ in range(300):
        if sp.run(f"curl -skf -o /dev/null https://{RELAY}:8444/healthz", shell=True).returncode == 0:
            break
        if RELAYP.poll() is not None:
            sys.exit(f"relay exited {RELAYP.returncode}: {OUT}/relay.log")
        time.sleep(0.1)
    pin = sh(f"openssl s_client -connect {RELAY}:8444 </dev/null 2>/dev/null | openssl x509 -outform der | sha256sum | cut -d' ' -f1")
    if PIN and pin != PIN:
        sys.exit(f"the restarted relay has another certificate ({pin}, was {PIN})")
    return pin


def relay_up():
    global PIN
    PIN = relay_start()
    for who in ("l", "d", "anon"):
        h = f"{W}/home-{who}"
        os.makedirs(h, exist_ok=True)
        ENV[who] = dict(os.environ, HOME=h, NFLTR_CONFIG=f"{h}/config.yaml", NFLTR_RELAY_CERT_SHA256=PIN)
    with open(f"{W}/relay/admin-enrol-code") as code:
        sp.run(["ip", "netns", "exec", "l", f"{B}/nfltr", "config", "set-relay", f"{RELAY}:8443", "--proxy-url",
                f"https://{RELAY}:8444", "--cert-sha256", PIN, "--code-file", "-"], stdin=code, env=ENV["l"], check=True,
               capture_output=True)


def relay_restart():
    kill(RELAYP)
    relay_start()


# ---- data ----

def data(who, size):
    """A random stream of size bytes, one per end and size, made once: the
    two ends' streams differ, so a byte that went the wrong way is caught."""
    os.makedirs(f"{W}/data", exist_ok=True)
    p = f"{W}/data/{who}{size}"
    if not os.path.exists(p):
        sh(f"head -c {size} /dev/urandom >{p}.tmp && mv {p}.tmp {p}")
    return p


def prefix_check(out, src):
    """None when out is a prefix of src (all of it or less), else the first
    differing offset."""
    n, m = os.path.getsize(out), os.path.getsize(src)
    with open(out, "rb") as a, open(src, "rb") as b:
        off = 0
        while True:
            x = a.read(4 * MiB)
            if not x:
                break
            y = b.read(len(x))
            if x != y:
                return off + next((i for i in range(min(len(x), len(y))) if x[i] != y[i]), min(len(x), len(y)))
            off += len(x)
    return None if n <= m else m


# ---- the ends ----

class End:
    """One nfltr-pipe process in its namespace: stdin from a file or a
    feeder (a function of the pipe), stdout to a file or through a reader
    (a function of the pipe and the file); its peak resident memory sampled."""

    def __init__(self, ns, args, env, src, out, log, feeder=None, reader=None):
        self.out, self.rss, self.t_exit = out, 0, None
        fi, fo, fe = None if feeder else open(src, "rb"), open(out, "wb"), open(log, "wb")
        self.p = sp.Popen(["ip", "netns", "exec", ns, f"{B}/{PIPE[ns]}"] + args, env=env, stdin=fi or sp.PIPE,
                          stdout=sp.PIPE if reader else fo, stderr=fe, start_new_session=True)
        for x in (fi, fo, fe):  # the child holds its own copies; out exists from here on for a reader
            if x:
                x.close()
        self.threads = []
        if feeder:
            self._go(feeder, self.p.stdin)
        if reader:
            self._go(reader, self.p.stdout, out)
        threading.Thread(target=self._sample, daemon=True).start()
        threading.Thread(target=self._reap, daemon=True).start()

    def _go(self, f, *a):
        t = threading.Thread(target=f, args=a, daemon=True)
        t.start()
        self.threads.append(t)

    def _reap(self):
        self.p.wait()
        self.t_exit = time.time()

    def _sample(self):
        # ip netns exec execs the command: p.pid is nfltr-pipe's; VmHWM is its peak so far.
        while self.p.poll() is None:
            try:
                for line in open(f"/proc/{self.p.pid}/status"):
                    if line.startswith("VmHWM:"):
                        self.rss = max(self.rss, int(line.split()[1]) // 1024)
            except (FileNotFoundError, ProcessLookupError, ValueError):
                pass
            time.sleep(0.05)

    def wait(self, t):
        try:
            rc = self.p.wait(timeout=max(0.0, t))
        except sp.TimeoutExpired:
            return None
        while self.t_exit is None:
            time.sleep(0.001)
        for th in self.threads:
            th.join(timeout=5)
        return rc

    def kill(self, sig=signal.SIGKILL):
        if self.p.poll() is None:
            os.killpg(self.p.pid, sig)


def kill(p):
    if p.poll() is None:
        os.killpg(p.pid, signal.SIGKILL)
    return p.wait()


def copy_reader(eof=None, rate=0, close_after=0):
    """A reader of an end's stdout into its file: at rate bytes/s when set,
    closing the pipe after close_after bytes when set, eof set at the end."""
    def run(pipe, out):
        n, t0 = 0, time.time()
        with open(out, "wb") as f:
            while True:
                b = pipe.read1(64 * 1024)
                if not b:
                    break
                f.write(b)
                n += len(b)
                if close_after and n >= close_after:
                    break
                if rate:
                    time.sleep(max(0.0, n / rate - (time.time() - t0)))
        pipe.close()
        if eof:
            eof.set()
    return run


def feeder(src, after=None, hold=None, first=0):
    """A writer of src into an end's stdin: once after is set, or first
    bytes, then the rest once hold is set; then its end."""
    def run(pipe):
        try:
            with open(src, "rb") as f:
                if after:
                    after.wait(TIMEOUT)
                if hold:
                    pipe.write(f.read(first))
                    pipe.flush()
                    hold.wait(TIMEOUT)
                while b := f.read(MiB):
                    pipe.write(b)
        except (OSError, ValueError):
            pass
        finally:
            try:
                pipe.close()
            except OSError:
                pass
    return run


# ---- one run ----

def run_cell(c, ld):
    """One session of cell c, with its fault if any. Returns the result row."""
    os.makedirs(ld, exist_ok=True)
    impair(c.get("rate", 0), c.get("rtt", 0), c.get("loss", 0))
    run = f"{W}/run"
    sh(f"rm -rf {run} && mkdir -p {run}")
    nl, nd = c["l"], c["d"]
    src = {"l": data("a", nl), "d": data("b", nd)}  # what each end's stdin holds
    out = {"l": f"{run}/l.out", "d": f"{run}/d.out"}
    f = c.get("fault") or {}
    relayed = c["path"] == "relayed"
    common = ["--direct", "off"] if relayed else []  # the pin: $NFLTR_RELAY_CERT_SHA256 (ENV), the same in every release
    want = {"l": c.get("expect_l", (0,)), "d": c.get("expect_d", (0,))}
    if os.environ.get("REL_EXPECT_D"):
        want = {"l": None, "d": (int(os.environ["REL_EXPECT_D"]),)}
    row = {"cell": c["name"], "bytes_l": nl, "bytes_d": nd, "fault": f or None}
    ends = []
    if c.get("noudp"):
        block_udp(True)
    counters(zero=True)
    eof, hold = threading.Event(), threading.Event()
    try:
        lfeed = lread = None
        if c.get("halfclose") == "dialler":  # the dialler's stdin ends at once; the listener sends after it read that end
            lfeed, lread = feeder(src["l"], after=eof), copy_reader(eof=eof)
        if f.get("kind") == "second":  # the listener holds its stream while a second dial is tried
            lfeed = feeder(src["l"], hold=hold, first=MiB)
        L = End("l", ["listen", "--relay", f"{RELAY}:8443", "--relay-http", f"{RELAY}:8444"] + common,
                ENV["anon" if c.get("anon") else "l"], src["l"], out["l"], f"{ld}/listen.log", lfeed, lread)
        ends.append(L)
        addr = f"{run}/addr"
        for _ in range(600):
            a = [x for x in open(f"{ld}/listen.log", errors="replace") if x.startswith("nf1")]
            if a or L.p.poll() is not None:
                break
            time.sleep(0.05)
        if not a:
            return dict(row, ok=False, reason=f"listen_exit_{L.p.poll()}" if L.p.poll() is not None else "listen_no_address")
        with open(addr, "w") as af:
            os.chmod(addr, 0o600)
            af.write(a[0])

        def dialler(name, srcf, outf, feed=None, read=None):
            return End("d", ["dial", "--address-file", addr] + common, ENV["d"], srcf, outf, f"{ld}/{name}.log", feed, read)

        dfeed = dread = None
        if c.get("halfclose") == "listener":
            dfeed, dread = feeder(src["d"], after=eof), copy_reader(eof=eof)
        if f.get("kind") == "slow":
            dread = copy_reader(rate=f["rate"])
        if f.get("kind") == "close":
            dread = copy_reader(close_after=f["after"])
        t0 = time.time()
        D = dialler("dial", src["d"], out["d"], dfeed, dread)
        ends.append(D)
        tf, killed = None, set()
        if f.get("kind") == "close":  # the fault is the reader's, from the start
            tf = t0
        if f.get("at") is not None:  # wait for the dialler to hold at% of the listener's stream
            at = f["at"] / 100 * nl
            while D.p.poll() is None and L.p.poll() is None and os.path.getsize(out["d"]) < at and time.time() - t0 < TIMEOUT:
                time.sleep(0.02)
            # On a direct cell the relay restart must find the session on the direct path.
            while f["kind"] == "relay" and not relayed and counters()[0] < MiB and D.p.poll() is None and time.time() - t0 < TIMEOUT:
                time.sleep(0.05)
            row["fault_applied"] = D.p.poll() is None and L.p.poll() is None
            if row["fault_applied"]:
                row["at_fault"] = os.path.getsize(out["d"])
                tf = time.time()
                if f["kind"] == "kill":
                    v = {"listener": L, "dialler": D}[f["victim"]]
                    v.kill(getattr(signal, f["sig"]))
                    if f["sig"] == "SIGKILL":
                        killed.add("l" if v is L else "d")
                elif f["kind"] == "drop":
                    impair(c.get("rate", 0), c.get("rtt", 0), 100)
                    time.sleep(f["s"])
                    impair(c.get("rate", 0), c.get("rtt", 0), c.get("loss", 0))
                elif f["kind"] == "relay":
                    relay_restart()
                elif f["kind"] == "second":
                    D2 = dialler("dial2", data("b", 0), f"{run}/d2.out")
                    ends.append(D2)
                    row["second_rc"] = D2.wait(90)
                    row["second_s"] = round(time.time() - tf, 2)
                    hold.set()
        rc = {"d": D.wait(TIMEOUT - (time.time() - t0))}
        # A listener whose dial never opened (anon-no-udp) waits on: it is not judged by its exit.
        rc["l"] = L.wait(5 if want["l"] is None else TIMEOUT - (time.time() - t0))
        secs = max([e.t_exit for e in (L, D) if e.t_exit] or [time.time()]) - t0
        direct, relay = counters()
        tot = max(1, direct + relay)
        row.update(rc_l=rc["l"], rc_d=rc["d"], seconds=round(secs, 3), direct_bytes=direct, relay_bytes=relay,
                   path_taken="direct" if direct / tot > 0.9 else "relayed" if direct / tot < 0.1 else "mixed",
                   rss_mb_l=L.rss, rss_mb_d=D.rss, got_l=os.path.getsize(out["l"]), got_d=os.path.getsize(out["d"]))
        if tf:
            row.update({f"exit_s_{k}": round(e.t_exit - tf, 2) for k, e in (("l", L), ("d", D)) if e.t_exit})
        if f.get("kind") == "second" and rc["l"] == 0 and rc["d"] == 0:
            D3 = dialler("dial3", data("b", 0), f"{run}/d3.out")
            ends.append(D3)
            row["third_rc"] = D3.wait(90)
        reasons, corrupt = [], False
        for k, peer in (("l", "d"), ("d", "l")):  # k's stdout against peer's stdin
            off = prefix_check(out[k], src[peer])
            if off is not None:
                reasons.append(f"{k}_corrupt")
                row[f"corrupt_at_{k}"] = off
                corrupt = True
            elif k in killed or want[k] is None:
                continue
            elif rc[k] is None:
                reasons.append(f"{k}_timeout")
            elif rc[k] == 0 and os.path.getsize(out[k]) != os.path.getsize(src[peer]):
                reasons.append(f"{k}_truncated")  # a cut stream taken for a whole one
                corrupt = True
            elif want[k] is not None and rc[k] not in want[k]:
                reasons.append(f"{k}_exit_{rc[k]}")
            elif tf and c.get("bound") and row.get(f"exit_s_{k}", 0) > c["bound"]:
                reasons.append(f"{k}_slow")
        for k, peer in (("l", "d"), ("d", "l")):  # k exited 0 ("the other side took every byte") and the
            # other end, alive, exited without them: what k sent was lost after the transport took it
            if rc[k] == 0 and peer not in killed and os.path.getsize(out[peer]) < os.path.getsize(src[k]):
                reasons.append(f"{k}_exit0_peer_short")
        for k, e in (("l", L), ("d", D)):
            if e.rss > RSS_MB:
                reasons.append(f"{k}_rss")
        if f.get("kind") == "second":
            if row.get("second_rc") not in f["second"]:
                reasons.append(f"second_exit_{row.get('second_rc')}")
            if "third_rc" in row and row["third_rc"] not in f["third"]:
                reasons.append(f"third_exit_{row['third_rc']}")
        if not reasons and nl + nd and not f and rc["l"] == rc["d"] == 0:
            row["mbit"] = round((nl + nd) * 8 / secs / 1e6, 1)
        return dict(row, ok=not reasons, reason=",".join(reasons) or None, corrupt=corrupt)
    finally:
        eof.set()
        hold.set()
        for e in ends:
            e.kill()
            e.wait(10)
        if c.get("noudp"):
            block_udp(False)
        sh(f"rm -rf {run}")


# ---- cells ----

def cells():
    out = []
    P = ("direct", "relayed")
    # Stream sizes, each direction alone and both at once, no impairment.
    for size, sn in ((0, "0"), (1, "1"), (64 * 1024 + 7, "64k7"), (16 * MiB, "16m")):
        for d, (nl, nd) in (("l2d", (size, 0)), ("d2l", (0, size)), ("both", (size, size))):
            for path in P:
                out.append(dict(name=f"size-{sn}-{d}-{path}", l=nl, d=nd, path=path))
    for path in P:
        out.append(dict(name=f"large-both-{path}", l=LARGE, d=LARGE, path=path))
    # Rate, round trip and loss, both directions at once.
    for rate in (10, 100, 1000):
        for rtt in (1, 40, 150):
            for loss in (0, 1):
                for path in P:
                    size = 4 * MiB if loss else {10: 4, 100: 16, 1000: 64}[rate] * MiB
                    out.append(dict(name=f"net-{rate}mbit-{rtt}ms-loss{loss}-{path}", l=size, d=size,
                                    rate=rate, rtt=rtt, loss=loss, path=path))
    # Faults: 64 MiB from the listener at 100 Mbit/s and 40 ms.
    f64 = dict(l=64 * MiB, d=0, rate=100, rtt=40)
    for victim in ("listener", "dialler"):
        survivor = "d" if victim == "listener" else "l"
        for at in (25, 50, 75):
            for path in P:
                # Killed (no reset sent): relayed the relay cuts the session at once, direct
                # the detach bound (about 30 s) does; the survivor exits 6. A listener whose
                # dialler's transport had taken its whole stream before the kill may exit 0:
                # exit 0 promises the hand-off to the transport (docs/design.md); the
                # dialler's stdout still has to be a prefix.
                surv = (0, 6) if survivor == "l" else (6,)
                out.append(dict(f64, name=f"kill-{victim}-{at}pct-{path}", path=path, bound=10 if path == "relayed" else 45,
                                fault=dict(kind="kill", victim=victim, sig="SIGKILL", at=at), **{f"expect_{survivor}": surv}))
        for path in P:
            # Stopped (SIGTERM): it resets; both exit 6 at once.
            out.append(dict(f64, name=f"term-{victim}-50pct-{path}", path=path, bound=10, expect_l=(6,), expect_d=(6,),
                            fault=dict(kind="kill", victim=victim, sig="SIGTERM", at=50)))
    for path in P:
        # One end's stdin ends first; the other end sends only after it read that end.
        for first in ("listener", "dialler"):
            out.append(dict(name=f"halfclose-{first}-first-{path}", l=0 if first == "listener" else 16 * MiB,
                            d=16 * MiB if first == "listener" else 0, path=path, halfclose=first))
        # The dialler's stdout closes after 1 MiB (| head): a stdout error resets; both exit 6.
        out.append(dict(f64, name=f"reader-closes-{path}", path=path, expect_l=(6,), expect_d=(6,), bound=10,
                        fault=dict(kind="close", after=MiB)))
        # The dialler's reader takes 4 MiB/s: the writer slows, memory does not grow.
        out.append(dict(name=f"slow-reader-{path}", l=32 * MiB, d=0, path=path, fault=dict(kind="slow", rate=4 * MiB)))
        # Four times the stream at twice the rate: the peak memory must not follow the stream.
        out.append(dict(name=f"slow-reader-128m-{path}", l=128 * MiB, d=0, path=path, fault=dict(kind="slow", rate=8 * MiB)))
        # The link drops (100 % loss both ways) at 30 %: a 3 s drop is survived. 10 s is
        # about the relayed path's liveness bound (link ping liveness): the session goes
        # on, or both ends exit 6 (never one 0 and one cut stream).
        out.append(dict(f64, name=f"drop-3s-{path}", path=path, fault=dict(kind="drop", s=3, at=30)))
        out.append(dict(f64, name=f"drop-10s-{path}", path=path, expect_l=(0, 6), expect_d=(0, 6),
                        fault=dict(kind="drop", s=10, at=30)))
        # A 60 s drop outlasts the liveness: both ends exit 6.
        out.append(dict(f64, name=f"drop-60s-{path}", path=path, bound=120, expect_l=(6,), expect_d=(6,),
                        fault=dict(kind="drop", s=60, at=30)))
        # The relay is killed and restarted at 30 %: a relayed session is cut (both 6);
        # a session on the direct path does not need the relay and completes.
        rr = dict(expect_l=(6,), expect_d=(6,), bound=60) if path == "relayed" else {}
        out.append(dict(f64, name=f"relay-restart-{path}", path=path, fault=dict(kind="relay", at=30), **rr))
        # One session per address: a second dial during the session is reset (6) and
        # the first goes on; a dial after it finds nothing (3).
        out.append(dict(name=f"one-session-{path}", l=16 * MiB, d=0, path=path,
                        fault=dict(kind="second", at=100 / 16, second=(6,), third=(3,))))
    # No account behind the address: direct only.
    out.append(dict(name="anon-direct", l=16 * MiB, d=16 * MiB, path="direct", anon=True))
    out.append(dict(name="anon-no-udp", l=16 * MiB, d=16 * MiB, path="direct", anon=True, noudp=True,
                    expect_l=None, expect_d=(12,)))
    out.append(dict(f64, name="anon-kill-listener-50pct", path="direct", anon=True, bound=45, expect_d=(12,),
                    fault=dict(kind="kill", victim="listener", sig="SIGKILL", at=50)))
    return out


# ---- aggregation ----

def med(xs):
    xs = [x for x in xs if x is not None]
    return round(statistics.median(xs), 2) if xs else None


def aggregate(rows):
    by = {}
    for x in rows:
        by.setdefault(x["cell"], []).append(x)
    agg = []
    for name, rs in by.items():
        ok = [x for x in rs if x["ok"]]
        reasons, paths = {}, {}
        for x in rs:
            if not x["ok"]:
                for r in str(x["reason"]).split(","):
                    reasons[r] = reasons.get(r, 0) + 1
            paths[x.get("path_taken") or "-"] = paths.get(x.get("path_taken") or "-", 0) + 1
        mb = [x["mbit"] for x in ok if x.get("mbit")]
        agg.append({"cell": name, "runs": len(rs), "pass": len(ok), "median_s": med([x.get("seconds") for x in ok]),
                    "median_mbit": med(mb), "min_mbit": min(mb, default=None), "max_mbit": max(mb, default=None),
                    "median_exit_s": med([max(x.get("exit_s_l", 0), x.get("exit_s_d", 0)) for x in rs if x.get("fault_applied")]),
                    "max_rss_mb": max([max(x.get("rss_mb_l", 0), x.get("rss_mb_d", 0)) for x in rs], default=0),
                    "paths": paths, "failures": reasons})
    return agg


def machine():
    return {"kernel": sh("uname -r"), "cpus": os.cpu_count(), "load": open("/proc/loadavg").read().split()[:3],
            "rmem_max": open("/proc/sys/net/core/rmem_max").read().strip(),
            **{f"nfltr-pipe_{n}_sha256": sh(f"sha256sum {B}/{b} | cut -d' ' -f1") for n, b in PIPE.items()},
            "nfltr": sh(f"{B}/nfltr version | head -1"), "nfltr-relay": sh(f"{B}/nfltr-relay version 2>&1 | head -1")}


def emit(path, row):
    with open(path, "a") as f:
        f.write(json.dumps(row) + "\n")


def table(agg):
    lines = ["| cell | pass | median s | median Mbit/s (min-max) | exit after fault, s | max RSS MiB | paths | failures |",
             "|---|---|---|---|---|---|---|---|"]
    for a in agg:
        mb = "-" if a["median_mbit"] is None else f"{a['median_mbit']} ({a['min_mbit']}-{a['max_mbit']})"
        lines.append(f"| {a['cell']} | {a['pass']}/{a['runs']} | {a['median_s']} | {mb} | {a['median_exit_s'] or '-'} | "
                     f"{a['max_rss_mb']} | " + ", ".join(f"{k} {v}" for k, v in a["paths"].items()) + " | "
                     + (", ".join(f"{k} {v}" for k, v in a["failures"].items()) or "-") + " |")
    return "\n".join(lines) + "\n"


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "matrix"
    if mode == "summarize":  # rel.py summarize <runs.jsonl> <out dir>: cells.jsonl and the table again
        agg = aggregate([json.loads(x) for x in open(sys.argv[2])])
        with open(f"{sys.argv[3]}/cells.jsonl", "w") as f:
            f.writelines(json.dumps(a) + "\n" for a in agg)
        with open(f"{sys.argv[3]}/summary.md", "w") as f:
            f.write(table(agg))
        return
    os.makedirs(f"{OUT}/logs", exist_ok=True)
    net_up()
    relay_up()
    started = time.time()
    meta = dict(mode=mode, started=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), n=N, machine=machine())
    rows = []
    sel = re.compile(os.environ.get("REL_CELLS", "."))
    try:
        for c in cells():
            if not sel.search(c["name"]):
                continue
            for i in range(N):
                if RELAYP.poll() is not None:
                    sys.exit(f"relay exited {RELAYP.returncode}")
                ld = f"{OUT}/logs/{c['name']}/{i}"
                row = dict(run_cell(c, ld), rep=i, at=time.strftime("%H:%M:%S"), load=open("/proc/loadavg").read().split()[0])
                rows.append(row)
                emit(f"{OUT}/runs.jsonl", row)
                print(json.dumps(row), flush=True)
                if row["ok"]:
                    sh(f"rm -rf {ld}")
        agg = aggregate(rows)
        for a in agg:
            emit(f"{OUT}/cells.jsonl", a)
    finally:
        kill(RELAYP)
        impair()
    fails = {}
    for x in rows:
        if not x["ok"]:
            for r in x["reason"].split(","):
                fails[r] = fails.get(r, 0) + 1
    summary = dict(meta, seconds=round(time.time() - started), sessions=len(rows), passed=sum(x["ok"] for x in rows),
                   corrupted=sum(1 for x in rows if x.get("corrupt")), failures=fails,
                   faults_applied=sum(1 for x in rows if x.get("fault_applied")),
                   cells_failed=[a["cell"] for a in agg if a["pass"] < a["runs"]])
    summary["ok"] = summary["passed"] == summary["sessions"] > 0
    with open(f"{OUT}/{mode}-summary.json", "w") as f:
        json.dump(summary, f, indent=1)
    with open(f"{OUT}/{mode}-summary.md", "w") as f:
        f.write(table(agg))
    print(json.dumps({k: summary[k] for k in ("ok", "sessions", "passed", "corrupted", "failures", "cells_failed")}))
    if os.environ.get("REL_UID"):
        sh(f"chown -R {os.environ['REL_UID']} {OUT}", check=False)
    sys.exit(0 if summary["ok"] else 1)


if __name__ == "__main__":
    main()
