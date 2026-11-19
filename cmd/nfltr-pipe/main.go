// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

// Command nfltr-pipe joins two machines' stdin and stdout over one end to
// end encrypted session: directly between them when their networks allow
// it, through an nfltr relay otherwise. One end listens and prints an
// address; whoever holds it dials.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nfltr/relay/client/agent"
	"github.com/nfltr/relay/client/nf1"
	"github.com/nfltr/relay/client/nfnet"
	"github.com/nfltr/relay/client/relaytrust"
)

const usage = `nfltr-pipe: join two machines' stdin and stdout, end to end encrypted

Usage:
  nfltr-pipe listen [flags]
  nfltr-pipe dial [flags] <address>
  nfltr-pipe dial [flags] --address-file <file>

listen prints an address (nf1...) on stderr and waits for one session:
its stdin goes to the dialler's stdout and the dialler's stdin to its
stdout. The address is a secret: whoever holds it can connect. Each
direction ends on its own: at its stdin's end the other side reads the
end of its stdout, and the other direction goes on until its own end. An
end exits 0 once both directions ended and the other side took every
byte it sent.

The session takes a direct path (QUIC) when the two networks allow one
and the relay carries it otherwise. With this machine's nfltr machine
certificate (the one ` + "`nfltr config login`" + ` left beside the nfltr config
file, or --machine-credential) the relay carries a session whose direct
path does not form; with none, listen serves with no account: direct paths
only, and a dial with no direct path exits 12.

dial takes the address as its argument (other users of this machine can
see a command line: --address-file keeps it in a file only you can read).

Exit status (the relay SDK's table):
  0  both directions ended and every byte was taken
  1  usage: bad flags, address or configuration
  2  auth: the relay refused this machine's certificate
  3  not_found: nothing listens at the address
  4  refused: the listener or the relay refused the session
  5  unreachable: the relay or the listener was not reached in time
  6  reset: the session was cut (the other end failed, was stopped or
     vanished, or this end's stdin or stdout failed, or it was stopped)
  7  e2ee_pin_mismatch: the listener is not the one the address names
  9  balance_exhausted: the serving account's balance is exhausted
  10 e2ee_client_refused: the listener refused this end's proof
  12 direct_required: no direct path to a listener serving with no account

Flags:
`

func main() { os.Exit(cli(os.Args[1:])) }

// cli runs one command line (args without the program's name) and
// returns its exit status.
func cli(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
	// A stdout closed early (| head) is a write error that resets the
	// session, not death by SIGPIPE, which would leave the other end
	// waiting for the SDK's liveness to notice.
	signal.Ignore(syscall.SIGPIPE)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "listen":
		return listenCmd(ctx, args[1:])
	case "dial":
		return dialCmd(ctx, args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "nfltr-pipe: unknown command %q\n\n%s", args[0], usage)
	return 1
}

// viaRelay is the relay configuration the commands end with: as given,
// except in the README examples' test, whose relay runs in the test.
var viaRelay = func(c nfnet.Config) nfnet.Config { return c }

// common are both commands' relay flags.
type common struct {
	relay, relayHTTP, pin, direct *string
}

func addCommon(fs *flag.FlagSet) common {
	return common{
		relay:     fs.String("relay", env("NFLTR_PIPE_RELAY", "grpc.nfltr.xyz:443"), "relay agent-link `address` (listen); $NFLTR_PIPE_RELAY"),
		relayHTTP: fs.String("relay-http", "", "relay HTTP `address` the address names; default: --relay without its grpc. prefix and port 443"),
		pin:       fs.String("relay-pin", os.Getenv(relaytrust.PinEnv), "accept the relay certificate with this SHA-256 `fingerprint` (a private relay's); $"+relaytrust.PinEnv),
		direct:    fs.String("direct", "on", "direct paths: `mode` on (LAN first, then the lowest round trip) or off (relayed only)"),
	}
}

func (c common) config() (nfnet.Config, nfnet.DirectMode, error) {
	mode, err := agent.ParseDirectMode(*c.direct)
	if err != nil {
		return nfnet.Config{}, nfnet.DirectOff, fmt.Errorf("--direct: %w", err)
	}
	if *c.pin != "" {
		if _, err := relaytrust.ParsePin(*c.pin); err != nil {
			return nfnet.Config{}, nfnet.DirectOff, err
		}
	}
	http := *c.relayHTTP
	if http == "" {
		http = strings.TrimPrefix(*c.relay, "grpc.")
	}
	// The address names the relay as its nf1.hosts do: no port 443.
	http = strings.TrimSuffix(http, ":443")
	return nfnet.Config{Relay: http, AgentRelay: *c.relay, RelayTLS: relaytrust.WithPin(nil, *c.pin)}, mode, nil
}

// stdio is an end's local side: in to the session, the session to out;
// out is closed at the session's end of the other direction (the reader
// of out sees its end there).
type stdio struct {
	in  io.Reader
	out io.WriteCloser
	log io.Writer
}

func osStdio() stdio { return stdio{os.Stdin, os.Stdout, os.Stderr} }

func listenCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("nfltr-pipe listen", flag.ContinueOnError)
	cf := addCommon(fs)
	machineFile := fs.String("machine-credential", "", "this machine's certificate `file` (machine.json, machine.key beside it); default: the one nfltr config login saved")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		return fail(os.Stderr, 1, errors.New("listen: no arguments"))
	}
	cfg, mode, err := cf.config()
	if err != nil {
		return fail(os.Stderr, 1, err)
	}
	m, err := loadMachine(machinePath(*machineFile))
	switch {
	case *machineFile == "" && errors.Is(err, errNoMachine): // serve with no account
		if mode == nfnet.DirectOff {
			return fail(os.Stderr, 1, errors.New("--direct off: with no machine certificate the address is served with no account, on direct paths only; run `nfltr config login` for the relay to carry it"))
		}
		m = nil
		fmt.Fprintln(os.Stderr, "no machine certificate: serving with no account, on direct paths only (a dial with no direct path exits 12; after `nfltr config login` the relay carries those)")
	case err == nil:
		if err = m.forRelay(cfg.AgentRelay); err != nil {
			return fail(os.Stderr, 1, err)
		}
		cfg.ClientCert = m.cert
		go m.run(ctx, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	default:
		return fail(os.Stderr, 1, err)
	}
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return listen(ctx, viaRelay(cfg), mode, m == nil, osStdio())
}

// listen serves one session under a fresh address, printed on sio.log
// with what to do with it, and pipes sio over it. anon: served with no
// account (cfg holds no credential), direct paths only.
func listen(ctx context.Context, cfg nfnet.Config, mode nfnet.DirectMode, anon bool, sio stdio) int {
	var id [4]byte
	_, _ = rand.Read(id[:])
	cfg.Agent = "pipe-" + hex.EncodeToString(id[:])
	l, _, err := nf1.Mint(cfg.Relay)
	if err == nil && mode != nfnet.DirectOff {
		l, err = l.WithDirect()
	}
	if err != nil {
		return fail(sio.log, 1, err)
	}
	ln, err := nfnet.Listen(ctx, cfg, "pipe", nfnet.ListenOptions{Address: l, Direct: mode})
	if err != nil {
		return fail(sio.log, codeOr(err, nfnet.CodeUnreachable), enrolHint(err, anon))
	}
	// Close resets a session still open and returns once the agent
	// stopped: the process may exit only then.
	defer ln.Close() //nolint:errcheck
	fmt.Fprintf(sio.log, "listening; give the other end this address (a secret: whoever holds it can connect):\n%s\n", ln.Address())
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	nc, err := ln.Accept()
	if err != nil {
		if ctx.Err() != nil {
			return fail(sio.log, int(nfnet.CodeReset), errors.New("stopped before a session opened"))
		}
		return fail(sio.log, codeOr(err, nfnet.CodeReset), enrolHint(err, anon))
	}
	c := nc.(*nfnet.Conn)
	go refuseOthers(ln) // one session per address
	return run(ctx, c, sio)
}

// refuseOthers resets every session after the first until ln closes: an
// address is one pipe.
func refuseOthers(ln *nfnet.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = c.(*nfnet.Conn).Reset()
	}
}

func dialCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("nfltr-pipe dial", flag.ContinueOnError)
	cf := addCommon(fs)
	addrFile := fs.String("address-file", "", "read the address from this `file` (mode 0600) instead of the command line")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return 1
	}
	var addr string
	switch {
	case *addrFile != "" && fs.NArg() == 0:
		a, err := readAddress(*addrFile)
		if err != nil {
			return fail(os.Stderr, 1, err)
		}
		addr = a
	case *addrFile == "" && fs.NArg() == 1:
		addr = fs.Arg(0)
	default:
		return fail(os.Stderr, 1, errors.New("dial: name the address, or --address-file"))
	}
	t, err := nfnet.ParseTarget(addr)
	if err != nil || t.Kind != nfnet.AddressTarget {
		return fail(os.Stderr, 1, fmt.Errorf("dial: want an nf1... address (%v)", err))
	}
	cfg, mode, err := cf.config()
	if err != nil {
		return fail(os.Stderr, 1, err)
	}
	cfg.Relay = t.Address.Relay // --relay-pin verifies the address's relay
	return dial(ctx, viaRelay(cfg), t, mode, osStdio())
}

// openBound bounds the dial's open: with direct paths past the SDK's
// direct-path bound (about 55 s), within which an address served with no
// account either forms its direct path or fails CodeDirectRequired.
func openBound(mode nfnet.DirectMode) time.Duration {
	if mode == nfnet.DirectOff {
		return 30 * time.Second
	}
	return 60 * time.Second
}

// dial opens the session at t and pipes sio over it.
func dial(ctx context.Context, cfg nfnet.Config, t nfnet.Target, mode nfnet.DirectMode, sio stdio) int {
	d, err := nfnet.NewDialer(cfg)
	if err != nil {
		return fail(sio.log, 1, err)
	}
	// The dialer's close drains its direct connections until the other
	// end closes them (up to a second): the process may exit only then.
	defer d.Close() //nolint:errcheck
	open, cancel := context.WithTimeout(ctx, openBound(mode))
	c, err := d.DialTarget(open, t, nfnet.DialOptions{Direct: mode})
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return fail(sio.log, int(nfnet.CodeReset), errors.New("stopped before the session opened"))
		}
		if nfnet.CodeOf(err) == nfnet.CodeDirectRequired {
			err = fmt.Errorf("%w (the listening end runs `nfltr config login`)", err)
		}
		return fail(sio.log, codeOr(err, nfnet.CodeUnreachable), err)
	}
	return run(ctx, c, sio)
}

// run pipes sio over c (spec/Pipe.tla): sio.in to c, CloseWrite at its
// end; c to sio.out, sio.out closed at c's end. It returns 0 once both
// directions ended and c's Close returned; at a failure of either side, a
// cut session or ctx's end it resets c (the other end reads a reset,
// never an end) and returns the cut's code, 6 for a local failure.
func run(ctx context.Context, c *nfnet.Conn, sio stdio) int {
	tr := newTracer()
	defer tr.write()
	errc := make(chan error, 2)
	var closing atomic.Bool // up's CloseWrite begun: from then on Done is the session's clean end
	go func() { errc <- up(c, sio.in, &closing, tr) }()
	// Its first end decides stdout: down's EOF closes it (the other
	// direction ended whole), a failure leaves it open (a Read after this
	// end's own Reset reads as an end).
	var outEnd sync.Once
	go func() { errc <- down(c, sio.out, &outEnd, tr) }()
	var err error
	ended := c.Done()
	for n := 0; n < 2 && err == nil; {
		select {
		case err = <-errc:
			n++
		case <-ended:
			// The session ended with this end's stdin still going: the
			// other end reset it or vanished, and nothing more of stdin
			// can arrive (a dial's Conn; an accepted one's Done is its own
			// Close, so a listener learns it at its next read or write).
			ended = nil
			if !closing.Load() {
				err = &nfnet.Error{Code: nfnet.CodeReset, Message: "session reset: it ended before this end's stdin did"}
			}
		case <-ctx.Done():
			err = errors.New("stopped")
		}
	}
	if err != nil {
		if errors.As(err, new(localError)) || ctx.Err() != nil {
			tr.ev("LocalFail")
		} else {
			tr.ev("RecvRst")
		}
		outEnd.Do(func() {})
		_ = c.Reset()
		return fail(sio.log, codeOr(err, nfnet.CodeReset), err)
	}
	tr.ev("Finish")
	if err := c.Close(); err != nil {
		tr.ev("RecvRst")
		return fail(sio.log, codeOr(err, nfnet.CodeReset), err)
	}
	tr.ev("Closed")
	return 0
}

// localError is a failure of this end's stdin or stdout.
type localError struct{ error }

func (e localError) Unwrap() error { return e.error }

// up copies in to c, then half-closes c (closing set first).
func up(c *nfnet.Conn, in io.Reader, closing *atomic.Bool, tr *tracer) error {
	if _, err := io.Copy(noted{c, tr, "Send"}, readerOnly{in, true}); err != nil {
		return err
	}
	closing.Store(true)
	if err := c.CloseWrite(); err != nil {
		return err
	}
	tr.ev("CloseWrite")
	return nil
}

// down copies c to out, then closes out unless run's failure came first.
func down(c *nfnet.Conn, out io.WriteCloser, end *sync.Once, tr *tracer) error {
	if _, err := io.Copy(noted{localWriter{out}, tr, "Recv"}, readerOnly{c, false}); err != nil {
		return err
	}
	tr.ev("RecvEnd")
	var err error
	end.Do(func() { err = out.Close() })
	if err != nil {
		return localError{fmt.Errorf("stdout: %w", err)}
	}
	return nil
}

// readerOnly hides WriterTo and types a local reader's failure.
type readerOnly struct {
	r     io.Reader
	local bool
}

func (r readerOnly) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF && r.local {
		err = localError{fmt.Errorf("stdin: %w", err)}
	}
	return n, err
}

// noted is a writer (hiding ReaderFrom) whose writes are the trace's ev
// (trace_on.go; nothing in a build without it).
type noted struct {
	w  io.Writer
	tr *tracer
	ev string
}

func (n noted) Write(p []byte) (int, error) {
	k, err := n.w.Write(p)
	if k > 0 {
		n.tr.ev(n.ev)
	}
	return k, err
}

type localWriter struct{ w io.Writer }

func (w localWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if err != nil {
		err = localError{fmt.Errorf("stdout: %w", err)}
	}
	return n, err
}

// codeOr is err's exit code on nfnet's table: def for a local failure or
// an error the table does not type.
func codeOr(err error, def nfnet.Code) int {
	if errors.As(err, new(localError)) {
		return int(def)
	}
	if code := nfnet.CodeOf(err); code != 0 {
		return int(code)
	}
	return int(def)
}

func readAddress(name string) (string, error) {
	// Windows reports every writable file as 0666 whatever its ACL says:
	// there the file is as private as its directory's ACL.
	if fi, err := os.Stat(name); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s: readable by others (chmod 600 it): an address is a secret", name)
	}
	b, err := os.ReadFile(name) // #nosec G304 -- the file the user named
	return strings.TrimSpace(string(b)), err
}

// enrolHint says what to do when the relay refused the certificate, or
// (anon) an address served with no account.
func enrolHint(err error, anon bool) error {
	switch code := nfnet.CodeOf(err); {
	case anon && code == nfnet.CodeAuth:
		return fmt.Errorf("%w; this relay serves no address without an account: run `nfltr config login` to serve with this machine's certificate", err)
	case code == nfnet.CodeAuth:
		return fmt.Errorf("%w; run `nfltr config login` to enrol this machine again", err)
	}
	return err
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(w io.Writer, code int, err error) int {
	fmt.Fprintln(w, "nfltr-pipe:", err)
	return code
}
