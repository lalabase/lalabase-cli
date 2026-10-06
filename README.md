# lalabase

Lalabase from the terminal: one binary for a person with arrow keys (a
full-screen interface) and for scripts and Claude Code (plain text or JSON).

**Status:** foundation (slice C0). Nothing to use day to day yet.

## Setting up

```sh
lalabase auth import            # take a server and token from Claude Code into the profile "default"
lalabase auth import --agent    # a read-only token for Claude Code into the profile "agent"
lalabase auth status            # who the tokens belong to and what the server supports
```

* **A profile is a server and a token, always together.** They live in
  `~/.config/lalabase/config.toml` (or under `$XDG_CONFIG_HOME`), which is
  refused when anybody but its owner may read it.
* The interactive mode takes `LALABASE_URL` plus `LALABASE_TOKEN` (both or
  neither), else the profile `default`. Agent commands take
  `LALABASE_AGENT_URL` plus `LALABASE_AGENT_TOKEN`, else the profile `agent`,
  never the person's profile, and refuse a token that may write, or one whose
  server does not say whether it may (before MCP 0.7.0), unless the profile
  says `allow_write = true`. That line lets both through, on purpose: the
  person allowed the agent to write. It is set by hand only; `auth import`
  never sets it and drops it, with a notice, when it replaces a profile.
  `--profile NAME` picks one profile of the file, whole.
* `auth import` reads Claude Code's MCP configuration (user, local and project
  scope), lists what it finds with only the token's prefix, always asks which
  entry to take, and checks the token with the server before saving. It runs
  nothing it finds and expands no variables. An entry from the current
  directory's `.mcp.json` (a cloned repository's, perhaps) is marked, with
  what `~/.claude.json` records about its approval; Claude Code's effective
  settings are not read, and the mark says so. It asks only on a terminal, an
  answer is one short line, and the list and the questions go to stderr (the
  result to stdout). Its requests go through the request queue like all
  others; a rate-limit pause is shown with its end, and a pause that outlasts
  the command's limit (30 s per profile) ends the command with that reason.
  Saving loads, merges and replaces the file under a lock (a regular file
  beside it, never a link), and only if the profile is still what was asked
  about; a file that would outgrow the read limit is not written.
* **The config's way is checked** against one threat: another local user
  without root, who can change only what is writable for them and replace
  only entries they own. From `/` down, links resolved as the kernel does:
  a directory passed through belongs to the user or root and is not writable
  by group or others unless sticky (`/tmp`); a link passed through belongs to
  the user or root; the config's own directory, the config and its lock file
  belong to the user and are writable by nobody else, sticky or not (the
  config and its lock file not readable either: reading is enough to hold a
  lock); the
  config is never a link. On macOS access control lists are read too (they
  can grant what the mode bits deny): an entry that allows others anything
  on the config, its lock file or its directory, or that lets them add,
  delete or rename further up, is refused; deny entries are fine. On Linux
  an access list shows in the mode bits (its mask), so the mode checks see it.
  Not covered: an attacker with root, network file systems that map user
  ids, and changes by the user's own processes between the check and the
  open (there is no descriptor-bound walk).
* Files the client does not control (its config, Claude Code's files, a
  `.mcp.json`) are read through `internal/safefile`: regular files only,
  opened without blocking, up to a size limit; a larger file is an error,
  never cut.

## Build

```sh
go build ./cmd/lalabase
./lalabase version
```

Go version: see `go.mod`.

## Development

```sh
go vet ./...
go test -race ./...
golangci-lint run
go-licenses check ./... --include_tests --allowed_licenses=MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,0BSD
```

* **Before every merge the tests run once on a Mac.** The CI tests on Linux
  only; the code that only macOS runs (access lists, file locks and FIFOs,
  the terminal) is tested nowhere else. As soon as anybody develops without
  a Mac, the macOS job comes back into `.github/workflows/ci.yml`.

* **Foreign text** (titles, comments, names, server error texts) goes through
  `internal/clean` before it reaches the terminal, in every mode. Its test
  cases are those of `bin/lala` in the core repository, byte for byte, plus a
  test over every code point. The output is valid UTF-8 for a **UTF-8
  terminal**; an 8-bit terminal that reads C1 controls from single bytes cannot
  be protected by cleaning (ordinary letters carry such bytes once encoded).
  The interface (C1) and the agent commands (C4) will therefore refuse a
  locale that is not UTF-8; C0 does not check the locale yet.
* **No invisible characters in the repository:** `internal/sourceguard` reads
  every tracked text file. Write such characters as escapes or numbers.
* **The server contract** is a copy of `spec/fixtures/mcp_contract.json` of the
  core repository, with its origin in `internal/contract/testdata/SOURCE.json`.
  Refresh it when the client adopts a newer server:
  `go run ./tools/update-contract -core <path to the core checkout> -ref origin/main`.
* **Talking to the server** (`internal/transport`): REST readers under `/api/v1`
  and MCP tools under `/api/mcp`. Every answer gets one class (object, not
  found, no access, read-only, refused, invalid input, unauthenticated,
  protocol, temporary, canceled); the cache (`internal/cache`) and the
  interface decide from the class, never from the server's wording. An older
  server without the machine field is recognised only by its exact texts;
  anything else it says is a refusal. TLS is required except for localhost, a
  certificate that does not check out is a protocol error (never retried), and
  redirects are not followed. `Sent` says whether a request left the client, so
  a write with an unclear outcome is reloaded, not sent again.
* **Writes** need the idempotency key of the person's action (8 to 128
  letters, digits or `._:-`, a form the server keeps unchanged); an adapter
  refuses a write without one before sending. An answer to a write never
  replaces or drops the record: a refused write keeps it, a successful one
  keeps it and asks for a reload.
* **Adapters** (`internal/api`): one per tool and REST reader, each with a
  `Spec` naming the arguments it sends and the fields it needs back (present
  and not null; a field that may be null is marked `?`). The same
  spec drives feature detection (`internal/features`, from `tools/list` and the
  input schemas; the server version decides nothing), the runtime check of
  every answer, and the compatibility test against the contract copy.
* **The request queue** (`internal/queue`): at most two requests at once,
  background work in at most one of the two places; the person's actions go
  first; identical reads in flight share one request; after a 429 nothing
  starts until the window opens. Every received 429 pauses, also when its body
  broke off, was too large or the caller left; a window not in the future
  pauses 5 seconds. A job is
  retried at most once: a read on a temporary failure, a write only after a
  429 or when nothing was sent.
* **Dependencies** must be permissive (MIT, Apache-2.0, BSD, ISC); CI checks
  it. Today: `github.com/BurntSushi/toml` (MIT), `golang.org/x/term` and
  `golang.org/x/sys` (BSD-3-Clause); in tests only `github.com/creack/pty`
  (MIT).

## Release

Push a tag `v*` (a version: `v` plus digits, letters, `.`, `+`, `-`). The
release workflow runs the whole CI on it first, then builds macOS (arm64,
amd64) and Linux (amd64, arm64) without write access, and only the last job
attaches the archives with `checksums.txt` to a GitHub release.

## License

MIT, see `LICENSE`.
