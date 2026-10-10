# lalabase

Lalabase from the terminal: one binary for a person with arrow keys (a
full-screen interface) and for scripts and Claude Code (plain text or JSON).

**Status:** organisations, the unread inbox and the tickets (slices C1b and
C1c). The interface verifies the token, lists the organisations with their
unread counts, shows the unread notifications of one organisation page by
page, and per organisation its projects, the tickets of a project page by
page with a search, and a ticket with its description and the first page of
its comments and attachments. Further pages of comments, marking as read
and writes come with C1d, C1f and C2.

## Setting up

```sh
lalabase                       # open the terminal shell using the default profile
lalabase auth import            # take a server and token from Claude Code into the profile "default"
lalabase auth import --agent    # a read-only token for Claude Code into the profile "agent"
lalabase auth status            # who the tokens belong to and what the server supports
```

With no command, both stdin and stdout must be terminals and the first set
locale in `LC_ALL`, `LC_CTYPE`, `LANG` must explicitly name UTF-8. Pipes fail
with exit 2; they do not select an agent mode. `lalabase --profile default`
reads only the profile file, while `lalabase` also accepts the environment
pair below. Other profile names are refused by the interface.

The interface needs at least 24 columns and 8 rows, uses two columns from
80 and three from 120, and keeps its state on resize.

* **The start** asks `/api/v1/me` first; only after that succeeds it reads
  the server's capabilities (`initialize`, `tools/list`), the organisations
  and the global unread page 1 for the counters: five requests in all,
  none shared with anything else. A failed step shows its line and `R`
  repeats that step alone (and what waited for it); a 401 anywhere ends
  the session for good ("Session expired. Restart lalabase.").
* **Keys.** Nothing is loaded by moving the cursor: `↑`/`↓` highlight an
  entry of the chooser (per organisation its inbox, with the unread count,
  and its tickets), `Enter` opens it. In the inbox (25 rows a page; on a
  return the remembered page and row) `[`/`]` turn the server pages and
  `Enter` on a row shows its details from the row itself (no request). In
  the tickets `Enter` on a project opens its tickets (25 a page, `[`/`]`;
  on a return the remembered page, row and search), `/` opens a one-line
  search where every printable key is text, `Enter` sends it once and
  `Esc` discards it (a blank search clears the filter), and `Enter` on a
  ticket opens it. `Esc` goes back one level, `R` refreshes what is shown
  (the directory and the counters in the chooser, the open page, the open
  ticket), `PgUp`/`PgDn` move by a screen, `←`/`→` move the focus, `q`
  quits with 0, Ctrl-C and SIGINT with 130. The help line names exactly
  the keys that act, on as many lines as it takes.
* **The ticket pane.** When the cursor rests on a ticket row for 300 ms and
  the object pane is on the screen (three columns, or two with the list or
  the pane focused), the ticket is read as a preview: background work, at
  most six background starts per rolling minute (retries counted), held
  until the window rolls on when the budget is spent, canceled when the
  cursor moves on. `Enter` opens the ticket as the person's read and joins
  a pending preview: one request, both get the whole ticket. The pane shows
  the head, the description (cut by the server at 8,000 characters), the
  comments newest first (page 1 of 25, with the number of readable
  comments) and the attachments (page 1 of 10). On a server without the
  paging arguments the first 50 comments come oldest first and the first
  ten attachments, and the pane says how many there are in all. `Esc` goes
  back to the list and keeps the pane.
* **What the server supports** comes from its tool schemas, never from its
  version: without `organisation_id` only "All organisations" opens, first
  page only; without `list_notifications` the inbox is off and the counts
  are unknown ("?"); without `list_tickets` there are no tickets entries;
  without its `q` there is no search; without `get_ticket` the list opens
  nothing. A counter is unknown until a read observed it, and a newer read
  wins whatever order the answers arrive in.
* **The cache** (memory only) shows a page, a list or a ticket seen before
  at once, marked "Cached", and reloads it exactly once (a ticket's reload
  after the pause, as background work). Every option of a request is part
  of its key: a search, a page, the page of comments. A temporary failure
  keeps what was shown, marked "Possibly stale"; "not found" and "no
  access" remove it (a project with every page and search of it, a ticket
  with every variant); a refusal keeps it with the server's message; a 429
  is shown with its deadline (UTC) while the queue waits. A page that no
  longer exists after a refresh is corrected once to the last page.
* Startup profile errors and recovered panics exit with 1; terminal, locale
  and profile flag errors exit with 2. A panic in the client's background
  work (a shared request, the wake-up after a rate limit pause, a read for
  the interface) ends the program the same way: the terminal is restored
  first, then one fixed line is printed and the exit code is 1, also when
  it coincides with `q`. Work that ignores its cancellation is left behind
  after one second.

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

## Trying the interface without a server

`tools/inbox-fixture` is a loopback stand-in for a server, built from the
contract copy: two organisations, two pages of unread notifications for the
first, rows without a project, a ticket or an actor among them; the first
organisation's four recorded projects (the second has none), 30 tickets in
Website-Relaunch (two pages; #1 is the recorded ticket, #2 has nothing, #3
has 30 comments, #4 twelve attachments), 3 in Handbuch, 5 in Archiv. It
binds to 127.0.0.1 only, accepts one synthetic token (`inboxfixture.Token`)
and refuses everything but the reads of C1. Scenarios: `normal`, `older`
(no organisation filter, no paging of comments), `temporary` (page 2 of the
first organisation's inbox and of Website-Relaunch's tickets fails twice,
`R` succeeds), `429` (one short pause), `401` (the session expires, also on
a preview of ticket #2).

```sh
go run ./tools/inbox-fixture -scenario temporary -port 18089
```

In another terminal, with a profile of its own, so no real token is read:

```sh
fixture_dir="$(mktemp -d /private/tmp/lalabase-fixture.XXXXXX)"
mkdir -m 700 "$fixture_dir/config" "$fixture_dir/config/lalabase"
( umask 077; printf '[profiles.default]\nurl = "http://127.0.0.1:18089"\ntoken = "lalabase-inbox-fixture-only"\n' > "$fixture_dir/config/lalabase/config.toml" )
go build -o "$fixture_dir/lalabase" ./cmd/lalabase
env -i PATH=/usr/bin:/bin TERM=xterm-256color LC_ALL=en_US.UTF-8 XDG_CONFIG_HOME="$fixture_dir/config" "$fixture_dir/lalabase" --profile default
rm -r "$fixture_dir"
```

The PTY tests of `cmd/lalabase` run the same flow against the same handler
at 80 and 120 columns. The shipped binary does not carry the fixture or the
contract copy (`internal/sourceguard` holds that).

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

* **CI tests Linux and macOS.** The macOS job runs the terminal and affected
  platform packages with the race detector and a ten-minute job limit,
  the inbox fixture and its tool among them. Vet, lint, fuzzing and license
  checks run on Linux. A real terminal check at 80 and 120 columns,
  including resize and terminal restoration, is still required before
  merge; the fixture above is what it runs against.

* **Foreign text** (titles, comments, names, server error texts) goes through
  `internal/clean` before it reaches the terminal, in every mode. Its test
  cases are those of `bin/lala` in the core repository, byte for byte, plus a
  test over every code point. The output is valid UTF-8 for a **UTF-8
  terminal**; an 8-bit terminal that reads C1 controls from single bytes cannot
  be protected by cleaning (ordinary letters carry such bytes once encoded).
  The interface therefore refuses a locale that is not UTF-8. The existing
  auth commands do not check the locale; agent commands arrive in C4.
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
  Arguments a newer server brings (server slice S1: `organisation_id`, the
  pages and the full text of `get_ticket`) are optional in the spec, each
  checked by the feature that sends it, so an older server keeps the base
  feature. An answer to such a request must carry the fields it relies on
  (a page without `posts_pages` is a protocol error, not page 0 of 0). A
  missing or hidden comment asked for by `post_id` comes back as a refusal
  (`comment_not_found`), not as "not found": the cache would drop the ticket.
* **The request queue** (`internal/queue`): at most two requests at once,
  background work in at most one of the two places; the person's actions go
  first; identical reads in flight share one request; after a 429 nothing
  starts until the window opens. Every received 429 pauses, also when its body
  broke off, was too large or the caller left; a window not in the future
  pauses 5 seconds. A job is
  retried at most once: a read on a temporary failure, a write only after a
  429 or when nothing was sent. Background work (previews, their reloads,
  their retries) starts at most six times per rolling minute; what is held
  waits for the window to roll on, the person's actions are never counted
  or held, and a held background read a person joins starts as theirs. A
  shared request belongs to the program's
  lifetime, not to its first caller: it is canceled when its last caller
  leaves or the program stops its work (on quitting, or after a fatal error).
* **Dependencies** must be permissive (MIT, Apache-2.0, BSD-2-Clause,
  BSD-3-Clause, ISC, 0BSD). CI checks the actual imports, including tests;
  a terminal library or its transitive dependency receives no exception.

## Release

Push a tag `v*` (a version: `v` plus digits, letters, `.`, `+`, `-`). The
release workflow runs the whole CI on it first, then builds macOS (arm64,
amd64) and Linux (amd64, arm64) without write access, and only the last job
attaches the archives with `checksums.txt` to a GitHub release.

## License

MIT, see `LICENSE`.
