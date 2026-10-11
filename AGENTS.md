# AGENTS.md

Working notes for coding agents in this repository (Claude Code reads this
file through `CLAUDE.md`). The README says what the
program does; this file says how to change it without repeating mistakes the
reviews of C0 already found. Status: 2026-10-12, main at C2a (#16: the
ground for the writes of C2, with its review findings in #21) plus the
status colours (#18), the cursor bar (#19) and the scrollbar on free text
(#23); C7 (Claude Code beside the ticket: `x`, `internal/launch`, project
folders per profile) is this branch.

The plan lives in a private repository: `doc/terminal_client_plan.md`
(slices C0 to C6, server slices S0 to S3, an "As built" section per slice).
Read the section of the slice you work on before you start.

## Check before every push

```sh
gofmt -l . && go vet ./... && go test -race -count=1 -timeout 5m ./... && golangci-lint run
```

The CI runs the same plus a 20 s fuzz run of `internal/clean` and `go-licenses`
(see README "Development"). The CI tests on Linux only: code behind
`//go:build darwin` (access lists, file locks, FIFOs, the terminal) is tested
on a Mac before every merge, by running the tests here.

## Map

| Package | Job |
|---|---|
| `cmd/lalabase` | commands; everything a command reads or writes goes through `Env` (tests pass their own) |
| `internal/transport` | REST `/api/v1` and MCP `/api/mcp`; every answer becomes one `Answer` with one `Class` |
| `internal/api` | one adapter per tool or REST reader, each with a `Spec` |
| `internal/features` | which features a server has, from `tools/list` and input schemas |
| `internal/cache` | the decisions on an answer, from its class alone, the memory store they act on, and the journal of its changes the disk replays |
| `internal/diskcache` | the disk cache (C1e): one directory per profile, one lock per run, a replay of the journal, every file checked before it is an entry |
| `internal/privatefs` | the one threat model of the client's private files (the config, its lock, the disk cache): owners, modes, the directory chain, macOS access lists, locks |
| `internal/queue` | the one queue every request goes through |
| `internal/gateway` | reads from the queue to the interface: one typed message per waiter, `Scope`, which checks a result before it is cached, and the one write path (`Write`), which confirms a success only from the answer |
| `internal/worker` | the supervisor: the only place a goroutine is started, its panics become one fatal error |
| `internal/ui` | the root Bubble Tea model: owns every piece of state, issues reads through the gateway, builds the screen; `view/` renders it and knows nothing else |
| `internal/present` | the display values the views take: opaque, cleaned once, a cast cannot forge one |
| `internal/uitext` | every text of the interface, in English, in one place |
| `internal/terminal` | the UTF-8 locale check before raw mode |
| `internal/inboxfixture` | the loopback stand-in for a server, built from the contract copy; `tools/inbox-fixture` serves it for a person |
| `internal/profile` | server plus token; the config file, checked through `internal/privatefs` |
| `internal/safefile` | reading files the client does not control |
| `internal/claudecfg` | Lalabase entries in Claude Code's configuration (`auth import`), and every server Claude Code may use in a folder (`Visible`, C7) |
| `internal/launch` | Claude Code beside the ticket (C7): the server name, the folder, the command line, the AppleScript; the only package that starts a child process |
| `internal/clean` | making foreign text safe for a terminal |
| `internal/contract` | copy of the core's MCP contract; imported by tests only |
| `internal/sourceguard` | tests over the repository's own files |
| `tools/update-contract` | refreshes the contract copy from a core checkout |

## Rules (each one was a review finding at least once)

1. **Foreign text is cleaned where it is shown**, through `internal/clean`,
   in every mode. Titles, comments, names, file names and server error texts
   are foreign. The transport passes them on raw; cleaning twice or in the
   wrong place hides the one place that matters.
2. **Decide from the `Class`, never from the server's wording.** Unknown text
   from a server is a refusal, never "temporary". An older server (no machine
   field) is recognised only by its exact, complete texts, kept in the
   transport.
3. **One `Spec` is the rule** for feature detection, the runtime check of every
   answer and the contract test. Required fields are present and not null; a
   field that may be null carries `?`. An argument a newer server brings goes
   into `Spec.Optional` and is checked by the feature that sends it, so an
   older server keeps the base feature. Do not add a second check beside the
   spec.
4. **Writes:** an idempotency key from the person's action, refused before
   sending when missing. `Answer.Sent` decides a retry: when it is unclear
   whether the server acted, reload, do not resend. An answer to a write never
   drops the record.
5. **Every request goes through `internal/queue`**, also in `auth` commands.
   Every received 429 pauses, whatever became of its body. A job is retried
   at most once.
6. **One threat model per area, no exceptions.** For the client's own files
   (the config, its lock file, the disk cache): another local user without
   root, held in `internal/privatefs` for all of them (README "The config's
   way"). Round 4 of C0c added exceptions (sticky directories, links, root)
   and each one opened the next hole; round 5 replaced them with the one
   model. When a case seems to need an exception, check it against the
   model instead. A new file the client keeps goes through that package,
   not through a check of its own.
7. **Files the client does not control** are read through `internal/safefile`:
   regular file, opened without blocking, size limit, a larger file is an
   error, never cut.
8. **A profile is URL plus token, always together.** Agent commands never read
   the profile `default` or `LALABASE_TOKEN`.
9. **No invisible characters in the repository.** Write them as escapes or
   numbers (`"\u202e"`, `0x202e`). Writing a file can turn escapes into the
   characters themselves; `internal/sourceguard` catches it. A **new
   directory** with code must be added to the list in
   `internal/sourceguard/invisible_test.go`, or that test turns red.
10. **The contract copy is never edited by hand** (a checksum test refuses it).
    Refresh it with `go run ./tools/update-contract -core <core checkout> -ref origin/main`,
    then classify every new recorded case.
11. **Dependencies are permissive** (MIT, Apache-2.0, BSD, ISC, 0BSD), test
    dependencies included, and are named in the README. Before adding one,
    read its license and ask whether the standard library does it.
12. **Goroutines only through `worker.Supervisor.Start`** (the source guard
    allows `go` nowhere else in production code). The queue shares an
    answer, never a decoded value: every waiter decodes its own. A result is
    cached or shown only after `gateway.Scope.Receive` accepted it.
    Where the interface and its work reach, production code imports only
    from the guard's allowlist (`reachableImports`); a new import there is a
    decision about writing and goroutines, not a fix for a red guard. The
    three packages that keep the client's files (`internal/diskcache`,
    `internal/privatefs`, `internal/safefile`) have their file calls named
    one by one in `fileSystemImports`, the output and goroutine rules hold
    there too, and no other package gets those names. Likewise
    `launchImports` names what `internal/launch` (`os/exec`) and
    `internal/claudecfg` may use; a child process starts nowhere else. No dot imports
    anywhere. A function the standard library runs on a goroutine of
    its own (an httptrace hook, a dial, a TLS callback) is set only through
    transport's `onWire` wrappers (`foreignCallbackTypes` in the guard; its
    fields are read from the types, in all production code). They raise its
    panic again on the request's goroutine, under the supervisor.
13. **No cgo.** macOS calls go through `golang.org/x/sys` (see `acl_darwin.go`).
    Platform code is split by build tags: `_unix`, `_darwin`, `_other`.

## Tests

* Test first for every bug and every review finding: the test is red before
  the fix.
* Server answers are **replayed from the contract copy** (`replayCase`,
  `replayOlder` in `internal/api`, `replay` in `internal/transport`), not
  written by hand. A hand-written answer only for what the contract cannot
  hold (a broken body, a TLS error, a 429 window).
* Time in the queue comes from a fake clock (`internal/queue/queue_test.go`);
  every test with goroutines has a deadline, so a hang fails instead of
  holding the run.
* A test that cannot set up its case skips locally but **fails on the CI**
  (`os.Getenv("CI")`): a skip there passes without testing anything.
* Terminal output is asserted on bytes; the terminal is tested through a real
  pseudo-terminal (`github.com/creack/pty`).
* Ask "what change would this test miss?". A reviewer's mutation probes are
  run as given, not a selection of them.

## Reviews: keep them converging

C0 took 3 to 6 review rounds per PR. Most findings were real, but late rounds
also found what earlier rounds' fixes had opened. To keep the count down:

* State the threat model and the slice's scope in the PR body before the first
  review, and give both to the reviewer.
* A finding needs a concrete failing case (input, state, wrong result). One
  without goes into the plan's backlog, not into this PR.
* From round 3 on, ask of each finding: a bug in this slice, or a new
  requirement? A new requirement goes into the next slice.
* When fixes keep adding special cases, stop and look for the missing rule
  (rule 6 above).

## Conventions

* Code comments, commits and PRs in English, plain words. Comments say why;
  package docs state the threat model where there is one.
* Commits: `feat: C1a ...`, `fix: review round N of #X, ...`, body as a list.
* Never commit on `main`; one branch per slice or fix. A second checkout lives
  beside this one as a worktree (`../lalabase-cli-<slice>`).
* Commits carry the GitHub noreply address (`git config user.email`), set in
  every new clone.
* A change in behaviour updates the README in the same PR, and after the merge
  the plan's "As built" section for the slice.
