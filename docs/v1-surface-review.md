# nfltr-pipe v1.0 surface review

Lane SPAPI, 2026-11-20, at pipe v0.1.4 (`e39832e`), beside nfltr-send
v0.1.9 (`7b6c86a`) and nfltr's `cat` commands (nfltr `57786a6`), as
link's review (link `docs/v1-api-review.md`) did for link. Every item a
1.x release keeps (docs/v1-contract.md) was asked: is it needed, named
well, documented with its failure modes, and the same as nfltr-send's
and nfltr cat's for the same idea? A change after 1.0 needs a 2.0, so
every "change" below is applied in this lane (CHANGELOG.md
"Unreleased"); `TestContractDoc` keeps the doc and the code equal.

## Findings and decisions

| # | kind | finding | decision |
|---|---|---|---|
| 1 | CLI | Go `flag` stops at the first argument: `nfltr-pipe dial nf1... --direct off` exited 1 ("name the address, or --address-file"). nfltr's commands take flags after their arguments (`nfltr cat forward home/db --listen ...`) | flags before or after the address; every word after the first `--` is an argument |
| 2 | repository | `.gitignore`'s `/nfltr-pipe` covers only a build at the root; nfltr-send committed its cmd build that way (26 MB, send `eb8ce4b`) | `.gitignore` names the cmd build; `TestNoTrackedBinaries` fails on any tracked ELF, Mach-O or PE file (none tracked today) |
| 3 | Go API | `package main` only: the module has no Go API | kept |
| 4 | JSON | nfltr-pipe writes no JSON; `nfltr cat pipe` writes JSON events on stderr | kept: the exit code is the contract, stderr's text is not covered; a `--json` with nfltr cat's event shape (`event`, `code`, `message`) would be a 1.x minor |
| 5 | CLI | dial takes the address as an argument; nfltr-send's receive and nfltr cat refuse it on the command line | kept: a pipe address is spent by its one session (a second dial is reset, a later one finds nothing) and the listener then exits; the contract and the usage text say a shared machine passes it with `--address-file` (a 0600 file, or `-`: stdin's first line) |
| 6 | CLI | the address is printed on stderr; nfltr-send prints it on stdout | kept: stdout is the session's bytes |
| 7 | exit codes | a stopped end (SIGINT, SIGTERM, SIGHUP) exits 6; nfltr-send's stopped sender 129/130/143 | kept: a pipe end stopped mid-session cut the session (6); send serves until stopped, so its stop must differ from a failure |
| 8 | naming | `--relay` / `$NFLTR_PIPE_RELAY` and `--relay-cert-sha256` here and in nfltr-send; nfltr's cat commands say `--server` / `$NFLTR_SERVER` and `--pin-cert` | kept here: the two products agree with each other and with `nfltr config set-relay --cert-sha256` |
| 9 | naming | `--machine-credential <file>` while every message says "machine certificate" | kept: the file (`machine.json`) holds the certificate and names its key file; nfltr-send uses the same flag |
| 10 | code | `machine.go` and `saved_relay.go` are the same file in send and pipe | no surface change; a later move into the relay SDK changes neither CLI |

Checked and kept as they are: the commands (`listen`, `dial`, `help`,
`version`), every flag's default and precedence, `$NFLTR_PIPE_RELAY`,
`$NFLTR_RELAY_CERT_SHA256`, `$NFLTR_CONFIG`, the exit table (the relay
SDK's, one table with nfltr-send and `nfltr cat pipe`; 8 left to
`nfltr cat`'s parent_gone), the address line's shape, the nf1 address
format and the release file names. nfltr-pipe writes no file but the
renewed `machine.json` (nfltr's format, written in place).

## Follow-up (not in this repository)

- nfltr: `nfltr cat`'s `--pin-cert` is `config set-relay --cert-sha256`'s
  and nfltr-pipe's `--relay-cert-sha256`; one name across nfltr is
  nfltr's decision.
