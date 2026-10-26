# lalabase

Lalabase from the terminal: one binary for a person with arrow keys (a
full-screen interface) and for scripts and Claude Code (plain text or JSON).

**Status:** organisations, the unread inbox, the tickets, marking as read
and a disk cache between runs (slices C1b to C1f and C1e: C1 is complete).
C2a lays the ground for the writes of C2 (a comment, the status, an
assignment, the title of a ticket): the contract copy at MCP 0.11.0, the
adapters of `update_ticket` and plain text comments, the features
`ticket_update` and `comment_plain` (`auth status` lists them), the
gateway's one write path and the fixture's writes; nothing new on the
screen yet. The interface verifies the token, lists the
organisations with their unread counts, shows the unread notifications of
one organisation page by page, opens the ticket a notification names, and
per organisation its projects, the tickets of a project page by page with a
search, and a ticket with its description, its comments page by page in
either order and its attachments page by page, the description or one
comment read whole on request. One `r` marks a notification as read, from
the inbox or from the ticket opened out of it. The other writes come with
C2. One `x` in a ticket opens Claude Code beside it (C7): in a new iTerm
pane, or without iTerm in this terminal until it ends. Since C9a the
client learns from the session's hooks what each of these agents does, and
shows it with a glyph in the ticket list and a line in the ticket's head.

## Setting up

```sh
lalabase                       # open the terminal shell using the default profile
lalabase auth import            # take a server and token from Claude Code into the profile "default"
lalabase auth import --agent    # a read-only token for Claude Code into the profile "agent"
lalabase auth status            # who the tokens belong to and what the server supports
```

`lalabase hook` is not for a person: the hooks of a session `x` started run
it (see "What the agents do").

With no command, both stdin and stdout must be terminals and the first set
locale in `LC_ALL`, `LC_CTYPE`, `LANG` must explicitly name UTF-8. Pipes fail
with exit 2; they do not select an agent mode. `lalabase --profile default`
reads only the profile file, while `lalabase` also accepts the environment
pair below. Other profile names are refused by the interface.

The interface needs at least 24 columns and 8 rows, uses two columns from
80 and three from 120, and keeps its state on resize. With three columns an
opened ticket or notification takes the navigation's room: the list keeps
its width, the object gets the rest, and `Esc` back to the list brings the
navigation again.

* **How it is drawn.** Every pane stands in a rounded frame with its title
  in the top border and a space inside either side; the pane in focus has a
  blue, bold frame and a bold title, the others a faint frame. The line
  under the header is a path to where you are: the places parted by `›`,
  the search and the page by `·`, as in
  `Nordlicht GmbH › Tickets › Website · search "login" · page 1 of 3` or
  `Nordlicht GmbH › Inbox · page 1 of 2`; in one column it names the panes
  (`Organisations › Tickets › Ticket`). A short hint (what `x` did: Claude
  Code opened or ended) takes the path's place for 5 seconds, then the path
  is back. A notice of a problem or a wait (an expired session, a
  rate-limit pause, the disk cache, an `x` running or failed) stands beside
  the path as long as its state holds. A notice or hint that does not fit
  takes the lines under it, from the panes,
  wrapped and whole. Only where that would leave the pane in focus no row
  is it cut, marked with "…". Its tone comes from the state it reports,
  never from its words: bold red when something did not work (an `x` that
  opened nothing or ended in an error, an expired session, the disk cache
  unusable), yellow while the client waits (a launch running, a rate-limit
  pause), plain for a hint; the text says the same without colour. Lists
  keep their values (a status, a count) in one right-aligned column, and
  the second line of an entry is faint; an inbox without unread
  notifications is faint, an unread count bold. Ticket numbers stand
  right-aligned as wide as the page's widest, so the titles and the lines
  under them start in one column. Every ticket list keeps two cells
  between the marker and the number for an agent's glyph (C9a), empty
  without one: the glyph is not faint in a done ticket's row, and in the
  cursor bar it takes the bar's colour, since its shape says the state.
  A ticket with an agent has one more line in its head, after the title
  and its facts: the glyph in its tone, then `Agent <state> · for <age>`. Free text wraps at 80
  columns at most, however wide the pane. Times are shown in the
  computer's time zone, to the minute, as "28 Aug 15:45", with the year
  when it is not the current one ("3 Nov 2025 09:12").
  A ticket's status,
  in the list and at the head of the ticket, takes the tone of what it means
  in its project, which the server sends as `status_functions`: cyan for new
  or open, yellow in progress, green done (completed or closed), faint in the
  backlog. A done ticket is faint in the list, its status green and faint,
  so the open ones stand out. Where the ticket pane is wide enough (76
  cells inside its frame, so from 158 columns with the list in focus, or from 120
  with the ticket in focus), the ticket's facts stand in a bar on its
  right, which does not scroll: Status in its tone, Project, People (the
  assignees, then the creator) and Updated, the labels faint, a long name
  cut with "…"; the head then drops its line of facts. In a narrower pane
  they stay in one faint line under the title. Comments by the token's own
  person show the author as "Name (you)", blue and bold; every other author
  is bold without colour. Only the user id from `/api/v1/me` decides, never
  a name, and without colour the "(you)" still says it. The client never guesses a meaning from a status name, since every
  project names its statuses its own way; a status without a function, or
  from a server before the field, has no tone. The colours are the
  terminal's own sixteen, so the theme picks the shade, and `NO_COLOR` turns
  them off. Bold, faint and colour only weigh the text: a terminal without
  them shows the same words. The cursor row of the focused pane is a bar,
  reversed over the pane's width (both lines of a two-line entry), in which
  faint and a status's tone give way and bold stays; the other panes draw
  their cursor row the same way on the terminal's grey (colour 8) instead of
  reversed. Both keep the marker `>`, so the cursor shows without any
  attribute. The help at the bottom draws each key as a key cap, reversed
  with a space either side, its description faint. Free text that is longer than its pane (a ticket,
  the details of a notification) carries a scrollbar in the pane's last
  column (beside a meta bar, the last column of the text): a thumb (`┃`) as long as the share in sight and where it is, on a
  faint track (`╎`); the thumb touches the bottom only at the end. The text
  always leaves that column and a space before it free, so it wraps the same
  whether the bar shows or not. In a ticket the bar covers the page of
  comments loaded, not the whole conversation.

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
* **The mouse wheel** scrolls the object pane (a ticket, the details of a
  notification) by three lines when the pointer is over it, whether the
  pane has the focus or stands beside the list; like `PgUp`/`PgDn` it lets
  go of the place the pane kept in sight. Anywhere else the wheel does
  nothing. The interface asks the terminal for button and wheel reports
  (modes 1002 and 1006, never every motion) and switches them off when it
  leaves, so selecting text needs the terminal's modifier while it runs
  (Option in iTerm2, Fn in Terminal.app).
* **The ticket pane.** When the cursor rests on a ticket row for 300 ms and
  the object pane is on the screen (three columns and two columns always,
  one column with the pane focused), the ticket is read as a preview: background work, at
  most six background starts per rolling minute (retries counted), held
  until the window rolls on when the budget is spent, canceled when the
  cursor moves on. `Enter` opens the ticket as the person's read and joins
  a pending preview: one request, both get the whole ticket. The pane shows
  the head, the description (cut by the server at 8,000 characters), the
  comments newest first (25 a page, with the number of readable comments
  the server counts) and the attachments (ten a page, each with its name,
  type, size and whether the server has its text; what the server did not
  send says "unknown", a missing size is never "0 B"). On a server without
  the paging arguments the first 50 comments come oldest first and the
  first ten attachments, and the pane says how many there are in all.
  `Esc` goes back to the list and keeps the pane. The head is the title in
  bold and one faint line of its facts (status, project, assignees,
  creator, last change); every section's heading is bold and ends in a
  rule; a comment is its author in bold, its time faint, and its text
  indented below; an attachment's type, size and text status are faint.
* **Paging the ticket (C1d).** `Tab` moves between the description, the
  comments and the attachments and brings the section's heading into
  sight; `[`/`]` turn the page of that section, `o` switches the comments
  between newest and oldest first (back to their page 1; the attachments'
  page stays), `↑`/`↓` select a comment (kept by its id across a reload,
  else the first). A text the server shortened ends with "Text shortened
  by server"; `e` reads it whole: the description (`full_text`, with the
  options of the page asked) or the selected comment (`post_id`). One
  expansion at a time; it is a read of its own and replaces no page; `Esc`
  closes it first, without a request, and a late answer does not open it
  again. A whole text can still be shortened by the server (100,000
  characters). Nothing is read by moving, previewing or switching
  sections: every page, order and expansion is the person's action, once
  per key (a key pressed again while its read is out does nothing). A page
  shown before comes from the cache at once and is read once again; one
  beyond the server's last page (the list shrank) is corrected once, both
  sections in one request, a second shrink stops and offers `R`, an empty
  section goes to page 1 without a request. `R` reloads the page shown; an
  open expansion stays as it is.
* **The ticket a notification names (C1f).** In the details of a
  notification `o` opens the ticket it names (its project and ticket id;
  nothing is guessed from a title or a message) in the object pane, with
  the notification as its origin: the head says "Notification: unread", and
  the ticket's own keys work as in the tickets section (`Tab`, `[`/`]`, `o`
  for the order, `e`, the comment selection, `R` for the ticket). `Esc`
  closes the ticket and returns to the details, or to the list when the row
  has left meanwhile. The origin is navigation state only: the same ticket
  opened from the tickets section has none, whatever an inbox visit cached,
  and `Enter` on another row, a page turn or another inbox drop the ticket.
* **Mark as read (C1f).** `r` marks the notification the focused pane
  shows: the selected row of the inbox (a row without a ticket too), the
  row whose details are open, or the notification the ticket in the pane
  was opened from. One key, no confirmation; opening, previewing, paging
  and expanding never mark. The permission comes from `/me` alone:
  `read_only` false offers the key, true hides and refuses it ("Read-only
  token"), a missing field hides it as "Write permission unknown", and a
  server without `mark_notification_read` as unsupported; the profile's
  `allow_write` plays no part. The target is captured when the key is
  pressed and never moves with the cursor: the answer for one row never
  marks another. The lock on that target is set before the request exists,
  so a second `r` does nothing until the outcome is known. Each press gets
  one idempotency key; the queue's one retry (the server not reached, or a
  429 on the first attempt) sends the same key and nothing else is ever
  sent again by itself. The outcome is shown on the row ("marking…",
  "marked", "not marked", "unknown"), in the details and in the ticket's
  head: a confirmed success (the server named the notification as read), a
  refusal with the server's text (read-only, missing, no access, archived;
  none of them removes a ticket), a write that was not done ("Not marked;
  the server was not reached. r try again"), or an unknown outcome
  ("Result unknown; refreshing", then "Result unknown"): the request left
  and no usable answer came back, so the server may have acted. A confirmed
  success and an unknown outcome drop every cached page of the
  organisation's inbox and of "All organisations", move their epochs on (a
  read begun before brings nothing old back), make their counters unknown
  and read the counters and the page shown once more; the counts are the
  server's, never counted down by the client, and a row missing from the
  reloaded page proves nothing. After the reads landed an explicit `r` on
  an unknown or refused target is a new operation with a new key; `R`
  reads, it never writes. A 401 on a write ends the session, also when its
  target was left meanwhile.
* **Claude Code beside the ticket (C7).** `x` in the ticket view (the
  focus on the ticket, the ticket shown whole) starts Claude Code with the
  ticket as its task. It gets the ticket's **address only**: a fixed German
  prompt with the number, `project_id`, `ticket_id` and the name of the
  MCP server it is handed, and reads the ticket itself with `get_ticket`;
  no title or other text of the server goes into the command line or the
  prompt. The ids pass only as UUIDs, the number only as a positive
  integer. Claude Code works with the rights of the profile's token,
  writes included; the prompt asks it to treat the ticket as foreign text
  and to ask before changing it, which is a request, not a boundary.
  * **Where.** In iTerm on macOS (`TERM_PROGRAM=iTerm.app`, a
    well-formed `ITERM_SESSION_ID`, and no tmux, GNU screen or zellij in
    between, whose session id may be stale) the client's own session is split
    vertically (AppleScript, a constant run with `osascript -`, the values
    as its arguments only), and the new pane's login shell runs the launch
    script, its path typed single-quoted (C7b). The script, run by `/bin/sh`
    whatever the person's shell is, does
    `cd '<folder>' || exit` and
    `exec claude --mcp-config '<dir>/m' --strict-mcp-config --session-id '<uuid>' --settings '<dir>/s' -n 'lalabase #<n>' '<prompt>'`,
    every value single-quoted; `claude` comes from the `PATH` the pane's
    shell exports (an alias or a shell function of that name is not used).
    Every press opens a new split. Elsewhere the interface gives the
    terminal to `claude` (from the `PATH`, no shell, the same arguments, no
    script) and comes back when it ends.
  * **Which folder.** `[profiles.<name>.folders]` in `config.toml` maps a
    project id to an absolute path or one under `~/`; without an entry,
    the folder `lalabase` was started in. The folder must exist; a path
    with a control character, a line break, a format character (such as a
    direction mark or a zero-width space), a line or paragraph separator,
    broken UTF-8 or a backslash is refused.
    ```toml
    [profiles.default.folders]
    00000000-0000-4000-8000-000000000031 = "~/dev/website"
    ```
  * **Which server (C7b).** Claude Code is handed the profile's server: the
    client writes it into a file of its own, `m`, with the profile's MCP
    address and token, under the name `lalabase_c7_<8 hex>` that no server
    of the person's has, and starts `claude` with `--mcp-config` on that
    file and `--strict-mcp-config`. The session therefore sees this one
    server and Claude Code's built-in browser tools, and none of its own
    configuration (`~/.claude.json`, a repository's `.mcp.json`) or of the
    claude.ai connectors; the client reads none of Claude Code's files for
    the launch. The server itself needs no approval; in Claude Code's
    `default` permission mode every tool call of it asks, since the
    person's allow rules name their own server, not this one.
  * **The launch files.** One directory per press,
    `~/.cache/lalabase/cli/<profile>/l/<16 hex>/` (or under
    `$XDG_CACHE_HOME`, beside the disk cache), holds `m` (0600), the
    settings with the agent's hooks `s` (0600, C9a) and, in iTerm, the
    script `run` (0700). They are kept like the client's config
    (`internal/privatefs`: the whole way checked, the directory the user's
    alone, every file new and private). A new file is removed if writing,
    syncing or checking it fails. `m` is written only once the launch can
    go on: without `claude` on the `PATH`, or with a directory path no
    shell takes as typed, there is none. The token is in `m` alone, as in
    `config.toml`: never in a process's arguments, the typed line or the
    script. Claude Code reads `m` and `s` once, at start. A launch directory older
    than a day goes before the next launch and when the client quits; a
    younger one stays.
  * **What the status line says:** opening, opened beside `#<n>` (or ended,
    without iTerm; both as a hint in the path's place for 5 seconds), or why not: the address, the folder, the
    launch files that could not be made (and why), `claude` not on the
    `PATH`, macOS refusing iTerm control
    (error -1743: allow it in System Settings, Privacy & Security,
    Automation), the session not found, no answer within 5 s (a pane may
    still open), or, as a problem, that Claude Code opened (or ran here and
    ended without an error) but its state cannot be shown, and why (C9a). While a
    launch runs, a second `x` does nothing.
* **What the agents do (C9a).** Every `x` makes an agent: an id of the
  client's own (Claude's session id changes with `/clear`, the agent stays)
  and a session id, which `claude` gets as `--session-id`.
  * **Where the hooks come from.** The launch writes one settings file per
    launch, `s` in its launch directory, and hands it over with
    `--settings`: a hook for each of `SessionStart`, `UserPromptSubmit`,
    `PreToolUse`, `PostToolUse`, `PermissionRequest`, `Stop` and
    `SessionEnd`, each one command without a shell operator,
    `'<lalabase>' hook --dir '<agents dir>' --event <event> <agent id>`,
    with a timeout of 5 s. They run beside the person's own hooks, whose
    configuration is not touched. Claude Code's `/hooks` does not list
    them; `/compact` shows a hook's command line, which holds no secret.
  * **What the hook does.** `lalabase hook` records the agent's state and
    nothing else: it writes to neither stdout nor stderr and always exits
    0 (output would reach the model, exit 2 would block a tool, any other
    code shows an error after every answer), reads no profile, no config
    and no token, asks no server, and of its environment reads
    `CLAUDE_PID` alone. Of the event's input, which carries the prompt and
    paths, it takes six checked values and shows none: the session id,
    whether a tool event carries a subagent's `agent_id`, whether
    `SessionStart`'s source is `clear`, `resume`, `fork` or `compact`,
    whether `SessionEnd`'s reason is one of the first three, whether a
    permission request
    is the question tool `AskUserQuestion`, and how many background tasks
    run; an input larger than 1 MiB or broken is not read, the event
    alone decides.
  * **The program must stay where it is.** The hooks name the program by
    its path, links resolved. Moved or removed while a session runs,
    Claude Code shows an error line after every answer. A program path or
    an agents directory with a character no shell takes as typed (as for
    the folder above), or a ticket without its organisation, launches
    without hooks, and the status line says why.
  * **Where the files are.** `~/.cache/lalabase/cli/<profile>/a/` (or under
    `$XDG_CACHE_HOME`), beside the launch directories and apart from them:
    per agent `<id>.launch.json` (the ticket's address, the pane, written
    once by the launch), `<id>.state.json` (the state, when it began, the
    latest session id, the pid and its start time, replaced whole by the
    hook under the lock `<id>.lock`). They are kept like the config
    (`internal/privatefs`); the hook never makes the directory. An agent
    stays until it is removed (a later slice); the launch directory's day
    and the disk cache do not touch them.
  * **The states**, in order of urgency (of several agents at a ticket the
    most urgent shows):

    | State | Glyph | Tone | From |
    |---|---|---|---|
    | waiting for approval | `!` | magenta, bold | `PermissionRequest` |
    | waiting for you | `⣿` | magenta | `Stop`, the question tool's request, or `SessionStart` after `/clear`, `/resume` or `/branch` (no `Stop` follows them) |
    | lost | `✕` | red | the pid is gone, or now has another start time |
    | working | braille spinner | cyan | `SessionStart` (a start, as `x`'s), `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop` with a background task running |
    | starting | `⠂` | cyan, faint | no hook yet (the folder's trust dialog comes first) |
    | ended | `✓` | green, faint | `SessionEnd`, but for `/clear`, `/resume` and `/branch` |

    A compaction (`/compact`, or Claude Code's own when the context is
    full, also in the middle of a turn) changes no state.
    A background subagent's tool events (they carry its `agent_id`) leave
    an open approval or question as it is: the session still waits.
    The age of a lost agent counts from the last state its hooks
    recorded: the client cannot know when the process went.
    The glyph and the word say the state; the tone only weighs it, and
    `NO_COLOR` keeps both. The client reads the files every 500 ms while
    an agent exists or a launch runs, otherwise not at all; the spinner
    moves one frame with every read.
  * **Two gaps.** Claude Code sends no event when a permission prompt is
    denied, nor on `Esc` during a tool: the first leaves "waiting for
    approval", the second "working", until the next prompt or the
    session's end. Both follow an action of the person in that pane, and
    the state's age shows how old it is.
* **A comment that is gone.** When the server answers a comment read whole
  with "not found" (it was deleted or made private), its cached text goes
  and is hidden at once, the ticket stays and says "Comment unavailable",
  and the ticket is read once without `post_id`: only that read may say
  the ticket is gone. "No access", or "not found" for the whole
  description, removes the ticket with every cached page and expansion.
  Without `full_text` and `post_id` there is no `e`; without the paging
  arguments no `[`/`]`/`o` in the ticket; each works without the other.
* **What the server supports** comes from its tool schemas, never from its
  version: without `organisation_id` only "All organisations" opens, first
  page only; without `list_notifications` the inbox is off and the counts
  are unknown ("?"); without `list_tickets` there are no tickets entries;
  without its `q` there is no search; without `get_ticket` the list opens
  nothing. A counter is unknown until a read observed it, and a newer read
  wins whatever order the answers arrive in.
* **The cache** shows a page, a list or a ticket seen before at once,
  marked "Cached", and reloads it exactly once (a ticket's reload after the
  pause, as background work). Every option of a request is part of its key:
  a search, a page, the page of comments. A temporary failure keeps what
  was shown, marked "Possibly stale" ("Cached, possibly stale" when the
  entry came from the cache); "not found" and "no access" remove it (a
  project with every page and search of it, a ticket with every variant); a
  refusal keeps it with the server's message; a 429 is shown with its
  deadline (local time) while the queue waits. A page that no longer exists after
  a refresh is corrected once to the last page. The memory holds 64 entries
  of at most 2 MiB each, 16 MiB in all; what it lets go stays on disk.
* **The disk cache (C1e)** keeps those entries between runs, under
  `~/.cache/lalabase/cli/<profile>/` (or `$XDG_CACHE_HOME/lalabase/cli/`
  when that variable names an absolute path; on macOS too). It is opened
  only after `/me` confirmed the identity, and an entry is filed under the
  profile, the normalised server address and the token id the server
  confirmed, never under a token or a fingerprint of one. There is no
  offline start: a run whose `/me` fails shows nothing of an earlier one,
  and the write permission comes from this run's `/me` alone. A page found
  on disk shows as "Cached" and is reloaded once like a page from memory; a
  hit, a restart or a failed reload extends no lifetime. The start makes
  its five requests as before; the disk adds none and saves none.
  * **Bounds:** 64 MiB per profile directory, 24 hours since the last
    successful network read (an entry stamped in the future is expired),
    2 MiB per payload, 2 MiB plus 64 KiB per file with its metadata, 1,024
    files. An answer above a bound is shown, not kept, and what the key held
    before goes with it. Pruning runs on open and when a write crosses a
    bound, never on a timer, in this order: temporary files and names of
    the cache's own that are no regular file; expired entries; then, while
    the directory is over its size or file count, the oldest entry first.
  * **Files:** the directory is made 0700 and checked like the config's
    directory (the whole way to it, see below; a directory others may list
    but not change passes, as for the config), the files 0600, one JSON
    record per entry with the format version, the key with its identity
    field by field, the time of the network read, the stale mark and the
    answer. A file is written to a unique temporary file beside it, synced
    and renamed over the old one; a reader sees the old or the new entry,
    never half of one, and a temporary file a crash left behind is never an
    entry. A file is used only when it is a regular file of the user's with
    no right for others, its record reads, its key is exactly the one asked
    for (a name proves nothing), it is young enough and its answer passes
    the same checks as a network answer; anything else is a miss and the
    file goes. Text from disk is cleaned on its way to the screen like text
    from the network.
  * **One lalabase at a time:** a run holds `lock` in the profile's
    directory for as long as it runs and never waits for it; on `q`,
    Ctrl-C or a fatal error it lets the lock go without waiting for a
    write that may be out (the write finishes or stops, then the lock
    goes). A second lalabase on the same profile says "Disk cache in use
    by another lalabase; using memory only" and runs on memory alone;
    another profile is another directory (with the environment pair the
    profile is `env`). With one writer the disk is a replay of the
    memory's decisions in their order (a batch at a time, bounded): a
    removal journaled before a write is applied before it, a write journaled
    before a removal never brings the data back, a disk read is made only
    after every change journaled before it is on disk, and a disk read that
    comes after a newer network answer, after a removal of its resource or
    after the selection moved on changes nothing. A reload that failed for
    a temporary reason before the disk answered leaves the entry the disk
    brought "possibly stale" in memory and on disk, not only on screen.
  * **Failure:** when the directory cannot be made or is unsafe, or the file
    system fails while reading or writing, the run says "Disk cache
    unavailable; using memory only" once in the status line and goes on
    with the network and memory; a single bad file is removed and is no
    such failure. A 401 removes every entry of the identity from the disk
    with the session; when that cannot be done the expired session says
    "The disk cache could not be cleared; remove it by hand".
  * **Clearing it by hand:** quit lalabase (the lock file is held only while
    it runs), then `rm -r ~/.cache/lalabase/cli/<profile>`. The next run
    makes the directory again.
  * **Known limits:** a token replaced by another leaves the old token id's
    entries on disk until they expire (a day at most; a 401 on the old token
    removes them, a new token does not know of them); a page opened in the
    first moments of a run, before the directory was opened, comes from the
    network alone; a directory beyond 8,192 entries is refused as unusable;
    a removal the file system refuses (other than a 401's) switches the
    disk off with the notice and leaves the file, so the next run shows
    that page as "Cached" once until its reload says again that it is gone.
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
  `--profile NAME` picks one profile of the file, whole. A profile may
  carry `folders` (C7, above), written by hand; `auth import` keeps them
  when it replaces the profile's server and token.
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
* **The config's way is checked** (`internal/privatefs`, the same model for
  the config, its lock file and the disk cache) against one threat: another
  local user without root, who can change only what is writable for them
  and replace only entries they own. From `/` down, links resolved as the
  kernel does:
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
has 30 comments, #4 twelve attachments, #6 everything C1d pages and
expands), 3 in Handbuch, 5 in Archiv; the first organisation's rows name
Website-Relaunch's tickets (row 6 names ticket #6), the second's name
tickets the fixture does not have. It
binds to 127.0.0.1 only, accepts one synthetic token (`inboxfixture.Token`)
and refuses everything but the reads of C1 and the writes of C1f and C2:
`mark_notification_read` (a marked row leaves the unread pages and the
counts), `update_ticket` (one change per call, validated in the server's
order with its texts and its trimming, ASCII and NUL as `String#strip`,
every Unicode space for a title; the status keys are the five of every
project, "me" is the person of `/me`, a user id in capitals or without
dashes names the same person, a stranger cannot be assigned) and
`post_comment` (plain or HTML; a plain body comes back as the server
stores and reads it, CR and CRLF as LF, the edge blank lines gone, a run
of blank lines as one paragraph break; an HTML body comes back as
ActionText reads it, a paragraph ending in two line breaks, `br` as one,
entities as characters; the author's unread rows naming the ticket are
read afterwards, as on the server). A change shows in the list, the
ticket and the summary from then on. A key seen before answers as the
server's dispatcher does: the original result for the same arguments,
without a second change, an invalid input for other ones, before the new
arguments are read, and the store is one for all three writes. The
fixture logs attempts (`Requests`), keys (`Keys`) and the changes it
carried out (`Mutations`, with their kind) apart, for the tests. A tool a
scenario took out of `tools/list` is unknown to the fixture, as to the
server, and a scenario standing for an older server names that server's
version in `initialize`. Scenarios:
`normal`, `older` (a 0.7.0 server: no organisation filter, no paging of
comments, no expansion, no `update_ticket`, no `format`), `s1-only` (a
0.9.0 server: no `update_ticket`, no `format`), `s2-only` (a 0.10.0
server: no `format`, so a comment is HTML to it), `temporary` (page 2 of the first organisation's inbox and of
Website-Relaunch's tickets fails twice, `R` succeeds; so does page 2 of
ticket #6's comments), `429` (one short pause, on page 2 and on the first
attempt of every write), `401` (the session expires, also on a preview of
ticket #2 and on a write), `vanished` (ticket #6's newest comment is still
on its page, but read whole it is "not found"), `read-only` (the token may
not write), `unknown-write` (`/me` without `read_only`), `no-mark-read`
(the tool is missing), `refusals` (marking the first organisation's row #2
is refused as archived, #3 is not found, #4 is not allowed; on
Website-Relaunch's tickets, #2 refuses a status and an assignment as
`value_not_allowed`, a title as `validation_failed` and a comment as
forbidden, #3 a status as `no_status_field`, #4 every write as forbidden,
#5 every write as vanished, #7 an assignment as `user_not_assignable`),
`lost` (every write is carried out and its answer is lost), `slow` (every
write is answered after 1.5 s), `slow-reads` (every inbox page of one organisation
and every ticket is answered after 1.5 s: what the disk cache shows
meanwhile after a restart). Ticket #6 of Website-Relaunch has a description and
two comments the server shortened, 30 comments and twelve attachments with
metadata of every kind (known, missing, zero bytes).

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
at 80 and 120 columns, the whole C1 run included (start, organisation,
inbox, details, the ticket a notification names, its pages and the
expansion, `r`, back to the updated inbox), and the disk cache across two
runs on one home (`slow-reads`: the inbox and the ticket "Cached" while
the reload is out, then fresh; `temporary`: "Cached, possibly stale" and
`R`; `401`: the directory cleared and nothing refilled; an unusable
directory and a busy lock). The shipped binary does not carry the fixture
or the contract copy (`internal/sourceguard` holds that).

To see the disk cache by hand, run the fixture with `-scenario slow-reads`,
start the client as above with a `$fixture_dir` of its own and
`XDG_CACHE_HOME="$fixture_dir/cache"` added to its environment, open an
inbox and a ticket, quit, and start it again: the pages show as "Cached"
for 1.5 s. The entries lie in `$fixture_dir/cache/lalabase/cli/default/`.

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
  checks run on Linux. Both jobs install the shells a pane may have (bash,
  zsh, fish): the launch tests run the typed line in each and fail on the
  CI when one is missing; locally a missing one is skipped. A real terminal check at 80 and 120 columns,
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
  keeps it and asks for a reload. Every write of the interface runs
  through the gateway's one path (`Write`, C2a; `r` of C1f is its first
  caller) as the person's job of kind Write, never shared, the key made at
  the key press from the session, the operation's number and the clock.
  The gateway confirms a success only when the answer carries the write:
  the notification asked for as read, the ticket asked for with the status
  or the title sent, the person among its assignees, a comment of that
  ticket; anything else after a request that left is an unknown outcome.
  `update_ticket` sends exactly one change (a status key, a title, or
  `"me"` to add; nobody is ever removed) and refuses a change of none or
  two before sending; `post_comment` sends `format: plain` only when asked
  (feature `comment_plain`), so an older server gets the request it knows.
* **Adapters** (`internal/api`): one per tool and REST reader, each with a
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
  contract copy is at MCP 0.11.0; a 0.9.0 or 0.10.0 server is shown as
  older than the tested version and works with what its schemas say. An answer to such a request must carry the fields it relies on
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
