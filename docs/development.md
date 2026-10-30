# Development

```sh
scripts/check fast   # while working: format, vet, go test -short (cached, no PTY)
scripts/check        # before a push: format, vet, go test -race, lint
go-licenses check ./... --include_tests --allowed_licenses=MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,0BSD
```

* **CI tests Linux and macOS.** The macOS job runs the terminal and affected
  platform packages with the race detector and a ten-minute job limit,
  the inbox fixture and its tool among them. Vet, lint, fuzzing and license
  checks run on Linux. Both jobs install the shells a pane may have (bash,
  zsh, fish): the launch tests run the typed line in each and fail on the
  CI when one is missing; locally a missing one is skipped. A real terminal check at 80 and 120 columns,
  including resize and terminal restoration, is still required before
  merge; the fixtures above are what it runs against.

* **Foreign text** (titles, comments, names, server error texts) goes through
  `internal/clean` before it reaches the terminal, in every mode. Its test
  cases are those of `bin/lala` in the core repository, byte for byte, plus a
  test over every code point. The output is valid UTF-8 for a **UTF-8
  terminal**; an 8-bit terminal that reads C1 controls from single bytes cannot
  be protected by cleaning (ordinary letters carry such bytes once encoded).
  The interface therefore refuses a locale that is not UTF-8. The existing
  auth commands do not check the locale; agent commands arrive in C4. What
  a person types or pastes into a field has a filter of its own in the same
  package, `clean.Draft` (M1), which the CI's fuzz run covers too.
* **No invisible characters in the repository:** `internal/sourceguard` reads
  every tracked text file. Write such characters as escapes or numbers.
* **Child processes** start in `internal/launch` alone (C7: `osascript` and
  `claude`): the source guard allows `os/exec` there, by name, and refuses
  it and the process calls of `os` and `syscall` in every other production
  file of the client, and every raw system call of `syscall` and
  `x/sys/unix` (any name with `Syscall` in it) but the one the access list
  reader makes in `internal/privatefs`. A syntax guard: a `go:linkname` to
  such a function is not seen.
* **No clock of its own** in the interface and in `internal/agents` (C9a):
  no tick, no timer, no sleep (but the hook's short retries for its lock);
  the agents are read every 500 ms through the gateway's Events, under the
  supervisor. The process table is read without cgo: `sysctl` through
  `x/sys/unix` on macOS, `/proc/<pid>/stat` on Linux.
* **The server contract** is a copy of `spec/fixtures/mcp_contract.json` of the
  core repository, with its origin in `internal/contract/testdata/SOURCE.json`.
  Refresh it when the client adopts a newer server:
  `go run ./tools/update-contract -core <path to the core checkout> -ref origin/main`.
* **Talking to the server** (`internal/transport`): REST readers under `/api/v1`
  (since K1 the ticket view's too) and MCP tools under `/api/mcp`. Every answer gets one class (object, not
  found, no access, read-only, refused, invalid input, unauthenticated,
  protocol, temporary, canceled); the cache (`internal/cache`) and the
  interface decide from the class, never from the server's wording. An older
  server without the machine field is recognised only by its exact texts;
  anything else it says is a refusal. TLS is required except for localhost, a
  certificate that does not check out is a protocol error (never retried), and
  redirects are not followed. `Sent` says whether a request left the client, so
  a write with an unclear outcome is reloaded, not sent again.
  A REST error of a server since the core's R0 carries a machine field
  (`code`), which alone places it: `invalid_input`, `read_only`,
  `forbidden` (no access), `not_found`, `refused` (with its `reason`) and
  `unauthorized`; a code the client does not know is a refusal. It counts
  only in the API's own form (a JSON object with a string `error` and a
  string `code`; `error` alone still places it as before when `code` is
  of another type): a
  proxy's 404 drops nothing from the cache. The two codes that drop a
  record count only with their own status (`not_found` with 404,
  `forbidden` with 403); beside another status they are a refusal without
  a reason. A 401, a 429, a 5xx and a
  redirect are placed by their status alone, as before. A server before R0
  (no `code`) is placed as it was: a 403 or 404 by its `error`, a 422 as
  an invalid input, any other client error as a protocol error.
* **Writes** need the idempotency key of the person's action (8 to 128
  letters, digits or `._:-`, a form the server keeps unchanged); an adapter
  refuses a write without one before sending. An answer to a write never
  replaces or drops the record: a refused write keeps it, a successful one
  keeps it and asks for a reload. Every write of the interface runs
  through the gateway's one path (`Write`, C2a; `r` of C1f is its first
  caller) as the person's job of kind Write, never shared, the key made at
  the key press from the session, the operation's number and the clock.
  The gateway confirms a success only when the answer carries the write:
  the notification asked for as read, the ticket asked for with the status
  or the title sent, the person among its assignees, a comment of that
  ticket, a booking of that ticket with an id and minutes above zero;
  anything else after a request that left is an unknown outcome.
  `update_ticket` sends exactly one change (a status key, a title, or
  `"me"` to add; nobody is ever removed) and refuses a change of none or
  two before sending; `post_comment` sends `format: plain` only when asked
  (feature `comment_plain`), so an older server gets the request it knows.
  A REST write (K0; the booking of K6 is the first) is a POST or
  PATCH under `/api/` with a JSON body and the key as its `Idempotency-Key`
  header; one with another method, no usable key or a body that is no JSON
  object is refused before sending, and its success is a JSON object
  (200 or 201). A retry of the same job encodes the same value the same
  way, so the server's replay, which compares the request, sees the same
  bytes; it answers a repeat with its first answer and another request
  under the same key as an invalid input. A write leaves once per attempt: Go's HTTP
  client, which would send a request with that header again by itself
  after a broken connection, is kept from it, and only the queue decides
  a retry.
* **Adapters** (`internal/api`): one per tool and REST route, each with a
  `Spec` naming the arguments it sends and the fields it needs back (present
  and not null; a field that may be null is marked `?`). The same
  spec drives feature detection (`internal/features`, from `tools/list` and the
  input schemas; the server version decides nothing), the runtime check of
  every answer, and the compatibility test against the contract copy.
  Arguments a newer server brings (server slice S1: `organisation_id`, the
  pages and the full text of `get_ticket`; S2b: `format` of
  `post_comment`) are optional in the spec, each checked by the feature
  that sends it, so an older server keeps the base feature. A tool a
  slice brings whole (S2: `update_ticket`) is a feature of its own, on
  only when every argument its spec declares is in the schema. The
  contract copy is at MCP 0.13.1, REST API 1.7.0; a 0.9.0 to 0.13.0
  server is shown as older than the tested version and works with what
  its schemas say. Fields a newer server sends are `Newer` in the spec:
  absent is fine, present they are checked like the required ones
  (`/api/v1/me` names `api_version` and `capabilities` since R0). A
  feature may name the REST capabilities its REST way needs; a server
  whose `/me` lists them all gets that way, any other the MCP way, so a
  server without capabilities keeps every feature. Since K1 the five
  features of the ticket view name theirs: the list and its search
  `tickets.list` with `tickets.paging` (the status catalogue of the list
  came with R1, `tickets.list` with R0), the ticket and its pages
  `tickets.show` with `tickets.paging`, the texts whole `posts.show` too.
  The booking (K6) is the first feature with the REST way alone: without
  `time_sessions.book` it is off, whatever tools the server lists. Every
  other feature takes the MCP way. A REST reader's spec is its
  tool's fields, under `data` for a ticket and one comment (the list has
  the tool's form as it is), so one rule serves both ways, and what a read
  files in the cache has the tool's form (a ticket's `data`; one comment
  alone, whichever way read it): an entry reads the same after a server
  moved to REST, and one comment the tools filed as a ticket before K1 is
  dropped and read again. A ticket is always asked over REST for a page
  (`posts_page`, the first when none is named): without one the core
  answers every comment and none of the counts. An id the server gave is
  refused before sending when it is no path segment. REST sends the
  attachments' extracted text whole, which the client does not read: a
  page of a ticket past the transport's bound of 8 MiB is a protocol
  error, and the cache keeps answers up to 2 MiB. An answer to such a request must carry the fields it relies on
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
  429 or when nothing was sent. Previews (and the one reload of a cached
  ticket shown) start at most 30 times per rolling minute and go before
  other background work, which is counted apart, at most six times. The
  core allows 60 requests a minute per IP address under `/api/`
  (`api/ip`, REST and MCP together), 60 per token on REST (`api_v1/token`)
  and 60 per token on MCP (`api_mcp/token`): the previews take at most
  half, the rest is your own actions and the Claude Code agents started
  with the same token on the same machine, which the client does not
  count; a 429 pauses everything, your actions too. The ticket view costs
  the same on both ways (K1): opening a project's list, opening a ticket,
  a page on and back are one read each (the open also reads the booked
  time, S6, through the tools); over REST the list's and the ticket's
  count against the REST token's budget instead of the MCP one the agents
  share, and against the address's as before. What is held waits for its window to roll
  on, the person's actions are never counted or held, and a held
  background read a person joins starts as theirs. Idle work (C9b: the
  titles of the agents' tickets) waits behind every preview that may
  start and every background job, and starts only while fewer than three
  of the six background starts are in the window (one that runs holds the
  background place meanwhile); a preview or a person joining an idle read
  lifts it. A
  shared request belongs to the program's
  lifetime, not to its first caller: it is canceled when its last caller
  leaves or the program stops its work (on quitting, or after a fatal error).
* **Dependencies** must be permissive (MIT, Apache-2.0, BSD-2-Clause,
  BSD-3-Clause, ISC, 0BSD). CI checks the actual imports, including tests;
  a terminal library or its transitive dependency receives no exception.
