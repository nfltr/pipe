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
	"runtime/debug"
	"slices"
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
  nfltr-pipe dial [flags] --address-file <file|->
  nfltr-pipe version

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

dial takes the address as its argument, or from --address-file: a file
only you can read, or - for stdin's first line (the rest of stdin is the
session's). Other users of this machine can see a command line: on a
shared machine use --address-file. dial takes no --relay: the address
names its relay.

Exit status (the relay SDK's table, shared with nfltr-send):
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
	// A hangup is a stop too (ssh sends one to its ProxyCommand when it
	// closes): untyped death would leave the other end to the liveness.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	switch args[0] {
	case "listen":
		return listenCmd(ctx, args[1:])
	case "dial":
		return dialCmd(ctx, args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintln(os.Stdout, versionLine())
		return 0
	}
	fmt.Fprintf(os.Stderr, "nfltr-pipe: unknown command %q\n\n%s", args[0], usage)
	return 1
}

// viaRelay is the relay configuration the commands end with: as given,
// except in the README examples' test, whose relay runs in the test.
var viaRelay = func(c nfnet.Config) nfnet.Config { return c }

// versionLine is the release (the module version the go command stamped
// from the tag), its commit and the Go version that built it.
func versionLine() string {
	v, commit := "(devel)", "unknown"
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" {
			v = bi.Main.Version
		}
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				commit = s.Value
			}
		}
	}
	return fmt.Sprintf("nfltr-pipe %s %s %s", v, commit, runtime.Version())
}

// common are the relay flags: --relay and --relay-http listen's only (the
// address names its relay).
type common struct {
	relay, relayHTTP, pin, direct *string
	saved                         savedRelay
}

// addCommon adds the relay flags (serving: listen's). The relay is
// --relay, else $NFLTR_PIPE_RELAY, else the one `nfltr config set-relay`
// saved (with its HTTPS URL and certificate pin), else grpc.nfltr.xyz:443.
func addCommon(fs *flag.FlagSet, serving bool) common {
	saved := loadSavedRelay()
	relay := defaultRelay
	if saved.Server != "" {
		relay = saved.Server
	}
	c := common{
		pin:    fs.String("cert-sha256", env(relaytrust.PinEnv, saved.Pin), "accept the relay certificate with this SHA-256 `fingerprint` (a private relay's); $"+relaytrust.PinEnv+", else the saved relay's"),
		direct: fs.String("direct", "on", "direct paths: `mode` on (LAN first, then the lowest round trip) or off (relayed only)"),
		saved:  saved,
	}
	if serving {
		c.relay = fs.String("relay", env("NFLTR_PIPE_RELAY", relay), "relay agent-link `address`; $NFLTR_PIPE_RELAY, else the relay nfltr config set-relay saved")
		c.relayHTTP = fs.String("relay-http", "", "relay HTTP `address` the address names; default: the saved relay's --proxy-url host with the saved relay, else --relay without its grpc. prefix and port 443")
	} else {
		c.relay = &relay // this machine's own relay: the dialler's direct paths' discovery falls back to it
	}
	return c
}

// parse parses args into fs quietly: -h prints the usage and fs's flags
// on stderr (help), and an error is the caller's to say: one line, a
// relay flag dial does not take named as such.
func parse(fs *flag.FlagSet, args []string) (help bool, err error) {
	fs.SetOutput(io.Discard)
	err = parseInterspersed(fs, args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(os.Stderr, usage)
		fs.SetOutput(os.Stderr)
		fs.PrintDefaults()
		return true, nil
	case err == nil:
		return false, nil
	}
	for _, f := range []string{"relay", "relay-http"} {
		if err.Error() == "flag provided but not defined: -"+f {
			return false, fmt.Errorf("dial takes no --%s: the address names its relay", f)
		}
	}
	return false, fmt.Errorf("%w (%s -h lists the flags)", err, fs.Name())
}

// parseInterspersed parses args into fs with flags before or after the
// arguments (as nfltr's commands take them); every word after the first
// -- is an argument. fs.Args() is the arguments, in order.
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var tail []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, tail = args[:i], args[i+1:]
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	return fs.Parse(append(append([]string{"--"}, pos...), tail...))
}

func (c common) config() (nfnet.Config, nfnet.DirectMode, error) {
	if *c.direct != "on" && *c.direct != "off" {
		return nfnet.Config{}, nfnet.DirectOff, fmt.Errorf("--direct %q: want on or off", *c.direct)
	}
	mode, err := agent.ParseDirectMode(*c.direct)
	if err != nil {
		return nfnet.Config{}, nfnet.DirectOff, fmt.Errorf("--direct: %w", err)
	}
	if *c.pin != "" {
		if _, err := relaytrust.ParsePin(*c.pin); err != nil {
			return nfnet.Config{}, nfnet.DirectOff, fmt.Errorf("--cert-sha256: %w", err)
		}
	}
	if c.relayHTTP == nil { // dial: the address names the relay
		return nfnet.Config{AgentRelay: *c.relay, RelayTLS: relaytrust.WithPin(nil, *c.pin)}, mode, nil
	}
	http := *c.relayHTTP
	if http == "" && *c.relay == c.saved.Server {
		http = c.saved.httpHost()
	}
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
	cf := addCommon(fs, true)
	machineFile := fs.String("machine-credential", "", "this machine's certificate `file` (machine.json, machine.key beside it); default: the one nfltr config login saved")
	if help, err := parse(fs, args); help || err != nil {
		if help {
			return 0
		}
		return fail(os.Stderr, 1, err)
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
		return fail(sio.log, codeOr(err, nfnet.CodeUnreachable), relayHint(enrolHint(err, anon), cfg.AgentRelay))
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
	cf := addCommon(fs, false)
	addrFile := fs.String("address-file", "", "read the address from this `file` (mode 0600), or - for stdin's first line, instead of the command line")
	help, err := parse(fs, args)
	if help {
		return 0
	}
	if err != nil {
		return fail(os.Stderr, 1, err)
	}
	cfg, mode, err := cf.config()
	if err != nil {
		return fail(os.Stderr, 1, err)
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
	cfg.Relay = t.Address.Relay // --cert-sha256 verifies the address's relay
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
	if name == "-" {
		return firstLine(os.Stdin)
	}
	// Windows reports every writable file as 0666 whatever its ACL says:
	// there the file is as private as its directory's ACL.
	if fi, err := os.Stat(name); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s: readable by others (chmod 600 it): an address is a secret", name)
	}
	b, err := os.ReadFile(name) // #nosec G304 -- the file the user named
	return strings.TrimSpace(string(b)), err
}

// firstLine reads r's first line a byte at a time, so that what follows
// it stays r's (the session's), at most 4 KiB.
func firstLine(r io.Reader) (string, error) {
	var b []byte
	c := make([]byte, 1)
	for len(b) < 4096 {
		n, err := r.Read(c)
		if n == 1 && c[0] != '\n' {
			b = append(b, c[0])
		}
		if (n == 1 && c[0] == '\n') || err == io.EOF {
			return strings.TrimSpace(string(b)), nil
		}
		if err != nil {
			return "", fmt.Errorf("stdin: %w", err)
		}
	}
	return "", errors.New("stdin: no address in its first 4 KiB")
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

// relayHint names the relay a connection was for and the flag that names
// another; a refused machine is enrolHint's (another relay is no fix).
func relayHint(err error, relay string) error {
	if nfnet.CodeOf(err) == nfnet.CodeAuth {
		return fmt.Errorf("relay %s: %w", relay, err)
	}
	return fmt.Errorf("relay %s (--relay names another): %w", relay, err)
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
