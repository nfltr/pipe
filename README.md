# nfltr-pipe

Join two machines' stdin and stdout over one end to end encrypted
session: directly between them when their networks allow it, through an
[nfltr](https://nfltr.xyz) relay otherwise. One end listens and prints an
address; whoever holds it dials.

```console
a$ nfltr-pipe listen
listening; give the other end this address (a secret: whoever holds it can connect):
nf1...
b$ nfltr-pipe dial nf1...
```

What one end types the other prints, both ways at once.

- **End to end.** The session is TLS 1.3 between the two machines, the
  listener's key pinned by the address; the relay sees neither the
  address's secret nor any byte of the stream.
- **Direct first.** Both ends try a direct path (QUIC, hole punched with
  the relay's help) and move the session to it when it works; the relay
  carries the session until then and whenever the direct path dies.
- **Exact.** The bytes on the session are the bytes of stdin: no framing,
  no encoding. Each direction ends on its own: at one end's stdin end the
  other's stdout ends after every byte, and the other direction goes on
  until its own end.
- **Ends that tell.** An end exits 0 only once both directions ended and
  the other side took every byte it sent. A failure anywhere (a stdin or
  stdout error, a signal, a cut session, a peer that vanished) resets the
  session: the other end exits non-zero and never sees a cut stream as a
  whole one. Exit codes are the relay SDK's typed table (below).

The design (who closes what and when, a peer that stops or vanishes,
buffer bounds) is [`docs/design.md`](docs/design.md), checked as a TLA+
model in [`spec/`](spec/README.md).

## Examples

Each example below runs in this repository's tests as written, both ends
on one machine (`cmd/nfltr-pipe/examples_test.go`). `addr` is a file
holding the listener's address, readable by its owner only (see
[Pass the address safely](#pass-the-address-safely)).

### Files

Copy a directory:

<!-- example: copy-dir -->
```console
src$ tar -C ~/project -cf - . | nfltr-pipe listen
dst$ nfltr-pipe dial --address-file addr | tar -C ~/project -xf -
```

tar on the receiving side reads to the end of the stream; `dial` exits 0
only after the sender's last byte and its end arrived. A transfer that
fails part way exits non-zero on both sides, and tar sees a truncated
archive, not a complete one.

Compress on the way (worth it on a slow link; the stream is encrypted, so
compress before it, not after):

<!-- example: zstd -->
```console
src$ tar -C ~/project -cf - . | zstd | nfltr-pipe listen
dst$ nfltr-pipe dial --address-file addr | zstd -d | tar -C ~/project -xf -
```

A disk or file image, with progress and rate on the sending side:

<!-- example: image -->
```console
src$ pv disk.img | nfltr-pipe listen
dst$ nfltr-pipe dial --address-file addr | dd of=disk.img bs=4M
```

Reading or writing a device instead (`pv /dev/sdb`, `dd of=/dev/sdb`)
needs root on that side.

### Databases and images

Dump a database straight into a restore, with no dump file on either
side:

<!-- example: sqlite -->
```console
src$ sqlite3 app.db .dump | nfltr-pipe listen
dst$ nfltr-pipe dial --address-file addr | sqlite3 app.db
```

The same shape works for any dump and restore pair that reads and writes
a stream, such as `pg_dump` and `psql`.

Move a container image between machines without a registry:

<!-- example: docker -->
```console
src$ docker save myapp:1.4 | nfltr-pipe listen
dst$ nfltr-pipe dial --address-file addr | docker load
```

### Commands

Follow a log on another machine:

<!-- example: follow-log -->
```console
server$ tail -F app.log | nfltr-pipe listen
laptop$ nfltr-pipe dial --address-file addr | grep --line-buffered ERROR
```

Stopping the reader (Ctrl-C) resets the session; the server's
`nfltr-pipe` exits 6.

Run commands on the listening machine and read their output (a shell for
whoever holds the address, for one session):

<!-- example: commands -->
```console
server$ mkfifo cmd.in; sh <cmd.in 2>&1 | nfltr-pipe listen >cmd.in
laptop$ printf 'uname -s\ndf -h .\n' | nfltr-pipe dial --address-file addr
```

`sh` runs what the dialler sends and exits at its end, which ends the
session; the dialler prints the output and exits 0.

Reach a TCP service on the listening machine, for one connection:

<!-- example: tcp-forward -->
```console
server$ socat EXEC:'nfltr-pipe listen' TCP:127.0.0.1:8080
laptop$ printf 'GET / HTTP/1.0\r\n\r\n' | nfltr-pipe dial --address-file addr
```

socat joins the listener's stdin and stdout to a connection to the
service; the dialler's stdin and stdout are the client's side of it.

Copy text from one machine's clipboard to another's (macOS `pbpaste` and
`pbcopy`; on Linux `xclip -o` and `xclip -i`, or `wl-paste` and
`wl-copy`):

<!-- example: clipboard -->
```console
a$ pbpaste | nfltr-pipe listen
b$ nfltr-pipe dial --address-file addr | xclip -selection clipboard -i
```

### ssh

ssh to a machine through a listener in front of its sshd (one ssh
session per address: run the listener again for the next one):

<!-- example: ssh -->
```console
server$ mkfifo sshd.in; sudo /usr/sbin/sshd -i <sshd.in | nfltr-pipe listen >sshd.in
laptop$ ssh -o ProxyCommand='nfltr-pipe dial --address-file ~/.ssh/server.addr' server
```

ssh still checks the server's host key and authenticates as usual; the
pipe only carries its connection. ssh stops its ProxyCommand with a
hangup as it closes, which resets the session: the server's `nfltr-pipe`
exits 6 then, unless the session had already finished (0). Anything
that runs over ssh works the same way, such as rsync:

<!-- example: rsync -->
```console
server$ mkfifo sshd.in; sudo /usr/sbin/sshd -i <sshd.in | nfltr-pipe listen >sshd.in
laptop$ rsync -a -e "ssh -o ProxyCommand='nfltr-pipe dial --address-file ~/.ssh/server.addr'" ~/project/ server:project/
```

### Scripts

Branch on the exit code: retry when the relay or the listener was not
reached in time (5), discard a copy that was cut (6):

<!-- example: exit-codes -->
```console
src$ nfltr-pipe listen < backup.tar
dst$ for try in 1 2 3; do
       nfltr-pipe dial --address-file addr > backup.tar; code=$?
       [ $code -eq 5 ] || break; sleep 10
     done
dst$ case $code in 0) echo complete ;; 6) rm backup.tar; echo cut ;; *) echo "failed: $code" ;; esac
```

### Pass the address safely

Paste the address into a file only you can read, so it never shows on a
command line or in shell history:

<!-- example: address-file -->
```console
src$ nfltr-pipe listen < backup.tar
dst$ umask 077; cat > addr    # paste the address, Enter, then Ctrl-D
dst$ nfltr-pipe dial --address-file addr > backup.tar
```

`dial` refuses an address file that others can read.

## Address

`listen` prints the address on stderr (stdout is the stream). It is a
capability: whoever holds it can connect, so pass it over a channel you
trust. `dial <address>` puts it on the command line, where other users of
the machine can see it; `dial --address-file <file>` reads it from a file
only its owner may read (a file others can read is refused, except on
Windows, where the file is as private as its directory's ACL). A listener
serves one session; a second dial of the same address is reset.

## Credentials

Dialling needs none. Listening needs none either, while the bytes can go
peer to peer:

- **With this machine's nfltr certificate** the listener registers its
  address under its account, and the relay carries a session whose direct
  path does not form. The certificate is the one `nfltr config login`
  saved beside the nfltr config file (`machine.json` and `machine.key`
  next to `$NFLTR_CONFIG`, else `~/.config/nfltr/`), or the `machine.json`
  that `--machine-credential` names (its `machine.key` beside it,
  readable by its owner only). The listener renews it while it listens.
- **With none** the address is served with no account: on direct paths
  only. The relay carries the session's signalling (its TLS handshake and
  the direct path's candidates, inside that TLS) and no byte of the
  stream. A dial whose direct path does not form exits 12
  (`direct_required`); `--direct off` with no certificate is refused.

## Flags and exit codes

```
nfltr-pipe listen [--relay host:port] [--relay-http host] [--relay-pin sha256] [--direct on|off] [--machine-credential file]
nfltr-pipe dial [--relay-pin sha256] [--direct on|off] <address> | --address-file <file>
```

`--relay` (default `grpc.nfltr.xyz:443`, `$NFLTR_PIPE_RELAY`) is the
relay the listener registers at; the address names it, so `dial` needs no
relay flag. `--relay-pin` (`$NFLTR_RELAY_PIN`) accepts a private relay's
certificate by its SHA-256 fingerprint. `--direct off` keeps the session
relayed.

| exit | code | meaning |
|---|---|---|
| 0 | | both directions ended and every byte was taken |
| 1 | usage | bad flags, address or configuration |
| 2 | auth | the relay refused this machine's certificate |
| 3 | not_found | nothing listens at the address (or it already served its session) |
| 4 | refused | the listener or the relay refused the session |
| 5 | unreachable | the relay or the listener was not reached in time |
| 6 | reset | the session was cut: the other end failed, was stopped or vanished, or this end's stdin or stdout failed, or it was stopped |
| 7 | e2ee_pin_mismatch | the listener is not the one the address names |
| 9 | balance_exhausted | the serving account's balance is exhausted |
| 10 | e2ee_client_refused | the listener refused this end's proof |
| 12 | direct_required | no direct path to a listener serving with no account, or that path was lost |

## Operating notes

- A peer that is killed (no reset sent) ends the other end's session: at
  once when relayed (the relay sees its connection end), within about 30 s
  on a direct path (the QUIC idle timeout); on an address served with no
  account that reads as the direct path lost, exit 12.
- A listener whose stdout already ended and whose stdin is idle learns
  that its dialler vanished at its next write; a dialler notices at once.
- Memory does not grow with the stream: one 32 KiB buffer per direction,
  and a write waits for the session's credit, so a slow reader slows the
  writer at the other end.
- On Linux quic-go, which carries the direct path, asks for UDP socket
  buffers larger than a default `net.core.rmem_max` allows and logs
  `failed to sufficiently increase receive buffer size` when it cannot get
  them ([quic-go: UDP buffer sizes](https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes)).
  Raise it (and `wmem_max`):

  ```console
  $ sudo sysctl -w net.core.rmem_max=7500000 net.core.wmem_max=7500000
  ```

## Install

Release binaries (linux, darwin, windows; amd64, arm64) are on
[Releases](https://github.com/nfltr/pipe/releases), each checked against
its signed `SHA256SUMS` and nfltr-pipe's own release key in
[`release/allowed_signers`](release/allowed_signers), Ed25519
`SHA256:f6CMaXVMYmUA1RGI6EmJ/0SNrAWN8xS7UQgCiXdwkaM` (`ssh-keygen` from
OpenSSH 8.1 or later):

```console
$ v=<version>; f=nfltr-pipe_linux_amd64
$ for x in $f SHA256SUMS SHA256SUMS.sshsig; do curl -fsSLO https://github.com/nfltr/pipe/releases/download/$v/$x; done
$ curl -fsSLO https://raw.githubusercontent.com/nfltr/pipe/main/release/allowed_signers
$ ssh-keygen -Y verify -f allowed_signers -I nfltr-release -n nfltr-release -s SHA256SUMS.sshsig < SHA256SUMS
$ grep " $f\$" SHA256SUMS | sha256sum -c && chmod +x $f && mv $f ~/bin/nfltr-pipe
```

On macOS, which has no `sha256sum`, check the sum with `shasum -a 256 -c` instead.

## Build

```console
$ go build ./cmd/nfltr-pipe
$ make check          # gofmt, vet, -race tests and the README examples (each against a relay core run in the test)
$ make spec           # the TLA+ model (spec/Pipe.tla), its witnesses and mutants
$ make spec-traces    # every session end of the tests checked against the model
$ make release-dryrun # the release files in dist/, built twice and compared, signed with a throwaway key
```

A release is `make release`, `make release-sign` (the key in
`~/.config/nfltr-pipe/release-signing-key`, or `SIGNING_KEY=<key>`),
`make release-verify` (against `release/allowed_signers`) on a clean
checkout of its tag, then the files in `dist/` attached to the tag's
GitHub release.

License: Apache-2.0.
