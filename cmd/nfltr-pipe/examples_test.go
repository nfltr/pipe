// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The README's examples run as written: each block after an
// `<!-- example: <id> -->` line is two machines' shell lines (`host$ ...`,
// unprefixed lines continuing the one above), run by bash on this machine,
// each host in its own home directory, with nfltr-pipe the test binary on
// a relay run in the test. The listening host runs first; the dialling one
// gets the address in the files its --address-file names (or on stdin, as
// a person pastes it), nf1... in its lines replaced by it. Each example's
// entry below sets up its input, and checks the outcome and exit codes.
//
// An example whose commands are missing here is skipped, and fails under
// PIPE_EXAMPLES_ALL=1 (the check workflow installs them all).
func TestReadmeExamples(t *testing.T) {
	blocks := readmeExamples(t)
	for id := range examples {
		if _, ok := blocks[id]; !ok {
			t.Errorf("example %q is not in the README", id)
		}
	}
	for _, b := range blocks {
		t.Run(b.id, func(t *testing.T) {
			ex, ok := examples[b.id]
			if !ok {
				t.Fatalf("README example %q has no entry in examples", b.id)
			}
			if ex.skip != "" {
				t.Skip(ex.skip)
			}
			for _, c := range ex.needs {
				if _, err := exec.LookPath(c); err != nil {
					if os.Getenv("PIPE_EXAMPLES_ALL") != "" {
						t.Fatalf("needs %s: %v", c, err)
					}
					t.Skipf("needs %s", c)
				}
			}
			runExample(t, b, ex)
		})
	}
}

// example is one README example's inputs and expected outcome.
type example struct {
	skip      string   // why it never runs here
	needs     []string // the commands it runs besides the shell's own
	setup     func(t *testing.T, h hosts)
	stdin     string // the dialling host's stdin
	pasteAddr bool   // the dialling host's stdin is the address
	interrupt string // Ctrl-C the dialling host once its stdout holds this
	codes     [2]int // the listening and dialling hosts' exit codes; -1 any
	check     func(t *testing.T, h hosts, out map[string]string)
}

// hosts are the example's machines' home directories, by name.
type hosts map[string]string

var examples = map[string]example{
	"copy-dir": {
		needs: []string{"tar"},
		setup: func(t *testing.T, h hosts) { tree(t, h["src"]+"/project"); mkdir(t, h["dst"]+"/project") },
		check: func(t *testing.T, h hosts, _ map[string]string) {
			sameTree(t, h["src"]+"/project", h["dst"]+"/project")
		},
	},
	"follow-log": {
		needs:     []string{"tail", "grep"},
		setup:     func(t *testing.T, h hosts) { appendLog(t, h["server"]+"/app.log") },
		interrupt: "ERROR",
		codes:     [2]int{6, -1},
	},
	"ssh": {
		needs: []string{"/usr/sbin/sshd", "ssh", "ssh-keygen", "mkfifo"},
		setup: func(t *testing.T, h hosts) { sshSetup(t, h["server"], h["laptop"]) },
		stdin: "echo $((6*7))\n",
		check: func(t *testing.T, _ hosts, out map[string]string) { contains(t, out["laptop"], "42") },
	},
	"rsync": {
		needs: []string{"/usr/sbin/sshd", "ssh", "ssh-keygen", "mkfifo", "rsync"},
		setup: func(t *testing.T, h hosts) { sshSetup(t, h["server"], h["laptop"]); tree(t, h["laptop"]+"/project") },
		check: func(t *testing.T, h hosts, _ map[string]string) {
			sameTree(t, h["laptop"]+"/project", h["server"]+"/project")
		},
	},
	"sqlite": {
		needs: []string{"sqlite3"},
		setup: func(t *testing.T, h hosts) {
			cmdOut(t, h["src"], "sqlite3", "app.db", "create table t(id integer primary key, v text); insert into t(v) values ('one'), ('two'), ('three');")
		},
		check: func(t *testing.T, h hosts, _ map[string]string) {
			if a, b := cmdOut(t, h["src"], "sqlite3", "app.db", ".dump"), cmdOut(t, h["dst"], "sqlite3", "app.db", ".dump"); a != b {
				t.Errorf("restored database differs:\n%s\nwant:\n%s", b, a)
			}
		},
	},
	"zstd": {
		needs: []string{"tar", "zstd"},
		setup: func(t *testing.T, h hosts) { tree(t, h["src"]+"/project"); mkdir(t, h["dst"]+"/project") },
		check: func(t *testing.T, h hosts, _ map[string]string) {
			sameTree(t, h["src"]+"/project", h["dst"]+"/project")
		},
	},
	"image": {
		needs: []string{"pv", "dd"},
		setup: func(t *testing.T, h hosts) { writeRandom(t, h["src"]+"/disk.img", 8<<20) },
		check: func(t *testing.T, h hosts, _ map[string]string) {
			sameFile(t, h["src"]+"/disk.img", h["dst"]+"/disk.img")
		},
	},
	"docker": {skip: "needs a docker daemon and an image to copy"},
	"commands": {
		needs: []string{"mkfifo", "uname"},
		check: func(t *testing.T, h hosts, out map[string]string) {
			contains(t, out["laptop"], strings.TrimSpace(cmdOut(t, h["laptop"], "uname", "-s")))
		},
	},
	"tcp-forward": {
		needs: []string{"socat"},
		setup: func(t *testing.T, _ hosts) { httpService(t, "127.0.0.1:8080", "hello from the service") },
		check: func(t *testing.T, _ hosts, out map[string]string) {
			contains(t, out["laptop"], "200 OK")
			contains(t, out["laptop"], "hello from the service")
		},
	},
	"clipboard": {skip: "needs a desktop session's clipboard"},
	"exit-codes": {
		setup: func(t *testing.T, h hosts) { writeRandom(t, h["src"]+"/backup.tar", 1<<20) },
		check: func(t *testing.T, h hosts, out map[string]string) {
			sameFile(t, h["src"]+"/backup.tar", h["dst"]+"/backup.tar")
			contains(t, out["dst"], "complete")
		},
	},
	"address-file": {
		setup:     func(t *testing.T, h hosts) { writeRandom(t, h["src"]+"/backup.tar", 1<<20) },
		pasteAddr: true,
		check: func(t *testing.T, h hosts, _ map[string]string) {
			sameFile(t, h["src"]+"/backup.tar", h["dst"]+"/backup.tar")
			if fi, err := os.Stat(h["dst"] + "/addr"); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("addr: %v, %v; want mode 0600", fi.Mode(), err)
			}
		},
	},
}

// block is one README example: each host's script, in the README's order.
type block struct {
	id      string
	order   []string
	scripts map[string]string
}

var (
	exampleMark = regexp.MustCompile(`^<!-- example: ([a-z0-9-]+) -->$`)
	hostLine    = regexp.MustCompile(`^([a-z]+)\$ (.*)$`)
	addrFile    = regexp.MustCompile(`--address-file ([^\s'"]+)`)
)

func readmeExamples(t *testing.T) map[string]block {
	t.Helper()
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := map[string]block{}
	lines := strings.Split(string(b), "\n")
	for i := 0; i < len(lines); i++ {
		m := exampleMark.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "```") {
			t.Fatalf("README line %d: an example mark not followed by a code block", i+1)
		}
		bl := block{id: m[1], scripts: map[string]string{}}
		last := ""
		for i += 2; i < len(lines) && !strings.HasPrefix(lines[i], "```"); i++ {
			if h := hostLine.FindStringSubmatch(lines[i]); h != nil {
				if _, ok := bl.scripts[h[1]]; !ok {
					bl.order = append(bl.order, h[1])
				}
				last = h[1]
				bl.scripts[last] += h[2] + "\n"
			} else if last != "" {
				bl.scripts[last] += lines[i] + "\n"
			} else {
				t.Fatalf("README example %s: %q is no host's line", bl.id, lines[i])
			}
		}
		if _, dup := blocks[bl.id]; dup {
			t.Fatalf("README example %s twice", bl.id)
		}
		blocks[bl.id] = bl
	}
	return blocks
}

func runExample(t *testing.T, b block, ex example) {
	r := testRelay(t)
	if len(b.order) != 2 {
		t.Fatalf("hosts %v: want two", b.order)
	}
	ln, dl := b.order[0], b.order[1]
	if !strings.Contains(b.scripts[ln], "nfltr-pipe listen") {
		ln, dl = dl, ln
	}
	h := hosts{}
	for _, n := range b.order {
		h[n] = mkdir(t, filepath.Join(t.TempDir(), n))
	}
	shims := shimDir(t)
	env := func(home string) []string {
		var e []string
		for _, kv := range os.Environ() {
			if k, _, _ := strings.Cut(kv, "="); k != "HOME" && k != "PATH" && !strings.HasPrefix(k, "NFLTR_") {
				e = append(e, kv)
			}
		}
		return append(e, "HOME="+home, "PATH="+shims+":"+os.Getenv("PATH"), "PIPE_GRPC="+r.GRPC, "PIPE_PROXY="+r.Proxy,
			"SSHD_CONFIG="+filepath.Join(home, "sshd_config"))
	}
	if ex.setup != nil {
		ex.setup(t, h)
	}

	var log syncBuf
	defer func() { t.Logf("stderr:\n%s", log.String()) }()
	lout := &syncBuf{}
	lcmd := shell(t, b.scripts[ln], h[ln], env(h[ln]), "", lout)
	lerr, err := lcmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	start(t, lcmd)
	addrc := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(lerr)
		for sc.Scan() {
			l := sc.Text()
			fmt.Fprintf(&log, "%s: %s\n", ln, l)
			if strings.HasPrefix(l, "nf1") {
				addrc <- l
			}
		}
	}()
	lexit := waiter(lcmd)
	var addr string
	select {
	case addr = <-addrc:
	case <-lexit:
		t.Fatalf("%s exited before printing an address", ln)
	case <-time.After(60 * time.Second):
		t.Fatalf("%s printed no address", ln)
	}

	script, stdin := strings.ReplaceAll(b.scripts[dl], "nf1...", addr), ex.stdin
	if ex.pasteAddr {
		stdin = addr + "\n"
	} else {
		for _, m := range addrFile.FindAllStringSubmatch(script, -1) {
			p := m[1]
			if rest, ok := strings.CutPrefix(p, "~/"); ok {
				p = filepath.Join(h[dl], rest)
			} else if !filepath.IsAbs(p) {
				p = filepath.Join(h[dl], p)
			}
			mkdir(t, filepath.Dir(p))
			if err := os.WriteFile(p, []byte(addr+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	dout := &syncBuf{}
	dcmd := shell(t, script, h[dl], env(h[dl]), stdin, dout)
	dcmd.Stderr = prefixed{&log, dl}
	start(t, dcmd)
	dexit := waiter(dcmd)
	if ex.interrupt != "" {
		for !strings.Contains(dout.String(), ex.interrupt) {
			select {
			case <-dexit:
				t.Fatalf("%s exited before its output held %q", dl, ex.interrupt)
			case <-time.After(100 * time.Millisecond):
			}
		}
		_ = syscall.Kill(-dcmd.Process.Pid, syscall.SIGINT)
	}
	for i, w := range []struct {
		name string
		exit <-chan int
	}{{ln, lexit}, {dl, dexit}} {
		select {
		case c := <-w.exit:
			if want := ex.codes[i]; want != -1 && c != want {
				t.Errorf("%s exited %d, want %d", w.name, c, want)
			}
		case <-time.After(120 * time.Second):
			t.Fatalf("%s did not exit", w.name)
		}
	}
	if ex.check != nil {
		ex.check(t, h, map[string]string{ln: lout.String(), dl: dout.String()})
	}
}

// shell is bash running script in its own process group (killed at the
// test's end, with whatever it left running).
func shell(t *testing.T, script, dir string, env []string, stdin string, stdout io.Writer) *exec.Cmd {
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir, cmd.Env, cmd.Stdout = dir, env, stdout
	cmd.Stdin = strings.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func start(t *testing.T, cmd *exec.Cmd) {
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
}

func waiter(cmd *exec.Cmd) <-chan int {
	c := make(chan int, 1)
	go func() { _ = cmd.Wait(); c <- cmd.ProcessState.ExitCode() }()
	return c
}

// shimDir holds nfltr-pipe (this test binary on the test's relay), sudo
// (no privilege: sshd with the test's config) and ssh (the test's client
// config, never the user's).
func shimDir(t *testing.T) string {
	d := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shims := map[string]string{
		"nfltr-pipe": fmt.Sprintf("PIPE_CHILD=cli exec '%s' \"$@\"", bin),
		"sudo":       `case "$1" in */sshd) exec "$@" -f "$SSHD_CONFIG";; esac; exec "$@"`,
	}
	if ssh, err := exec.LookPath("ssh"); err == nil {
		shims["ssh"] = fmt.Sprintf(`exec '%s' -F "$HOME/.ssh/config" "$@"`, ssh)
	}
	for n, s := range shims {
		if err := os.WriteFile(filepath.Join(d, n), []byte("#!/bin/sh\n"+s+"\n"), 0o700); err != nil { // #nosec G306 -- an executable
			t.Fatal(err)
		}
	}
	return d
}

// sshSetup gives server an sshd config (run as this user, in server's
// home) and client an ssh config whose key it accepts and whose known
// host "server" is that sshd.
func sshSetup(t *testing.T, server, client string) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS's sshd opens a session only as root (its audit session)")
	}
	mkdir(t, client+"/.ssh")
	cmdOut(t, server, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", "hostkey")
	cmdOut(t, client, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", ".ssh/id")
	write(t, server+"/authorized_keys", read(t, client+"/.ssh/id.pub"))
	write(t, client+"/.ssh/known_hosts", "server "+read(t, server+"/hostkey.pub"))
	write(t, server+"/sshd_config", fmt.Sprintf(`HostKey %[1]s/hostkey
AuthorizedKeysFile %[1]s/authorized_keys
StrictModes no
UsePAM no
LogLevel ERROR
ForceCommand cd %[1]s && exec /bin/sh -c "${SSH_ORIGINAL_COMMAND:-exec /bin/sh}"
`, server))
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	write(t, client+"/.ssh/config", fmt.Sprintf(`Host server
  User %s
  IdentityFile %[2]s/.ssh/id
  IdentitiesOnly yes
  UserKnownHostsFile %[2]s/.ssh/known_hosts
  StrictHostKeyChecking yes
  BatchMode yes
  LogLevel ERROR
`, u.Username, client))
}

// appendLog appends a log line to name every 100 ms until the test ends.
func appendLog(t *testing.T, name string) {
	write(t, name, "")
	done := make(chan struct{})
	var wg sync.WaitGroup
	t.Cleanup(func() { close(done); wg.Wait() })
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			case <-time.After(100 * time.Millisecond):
			}
			f, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return
			}
			fmt.Fprintf(f, "INFO request %d\nERROR request %d failed\n", i, i)
			_ = f.Close()
		}
	})
}

// httpService serves body on addr (skips the test when addr is taken).
func httpService(t *testing.T, addr, body string) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("%s: %v", addr, err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// tree writes a directory of random files, one in a subdirectory.
func tree(t *testing.T, dir string) {
	mkdir(t, dir+"/sub")
	writeRandom(t, dir+"/a.bin", 3<<20)
	writeRandom(t, dir+"/sub/b.bin", 1000)
	write(t, dir+"/notes.txt", "notes\n")
}

func sameTree(t *testing.T, want, got string) {
	t.Helper()
	n := 0
	err := filepath.WalkDir(want, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(want, p)
		sameFile(t, p, filepath.Join(got, rel))
		n++
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("walk %s: %d files, %v", want, n, err)
	}
}

func sameFile(t *testing.T, want, got string) {
	t.Helper()
	a, b := read(t, want), read(t, got)
	if a != b {
		t.Errorf("%s: %d bytes, not the %d of %s", got, len(b), len(a), want)
	}
}

func contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("output %q does not hold %q", s, sub)
	}
}

func cmdOut(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return string(out)
}

func mkdir(t *testing.T, d string) string {
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func write(t *testing.T, name, s string) {
	if err := os.WriteFile(name, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRandom(t *testing.T, name string, n int) {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	write(t, name, string(b))
}

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name) // #nosec G304 -- the test's own file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// prefixed writes each write to w after the host's name (the log's lines
// are by host).
type prefixed struct {
	w    io.Writer
	host string
}

func (p prefixed) Write(b []byte) (int, error) {
	_, _ = fmt.Fprintf(p.w, "%s: %s", p.host, b)
	return len(b), nil
}
