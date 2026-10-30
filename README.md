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
Since C9b it shows every agent of the profile across the organisations: an
Agents area under the organisations, a glyph at each organisation, a
summary in the header, and `A` to reach them. Since C9c `d` removes an
agent whose session is over, and every start cleans up old ones; `x` on a
ticket with an agent jumps to the agent's pane, or starts it again once
its session is over. Since K1 the ticket list and the ticket are read over
the core's REST API when its `/me` lists the capabilities of the core's R1,
and through the MCP tools on an older server; the screen is the same
either way. M1 lays the ground for what a person writes: a box over the
screen with input fields (see "The dialog"). Since K6 `b` on a ticket books
time through it, the first write over REST (see "Booking time").

## Setting up

```sh
lalabase                       # open the terminal shell using the default profile
lalabase auth import            # take a server and token from Claude Code into the profile "default"
lalabase auth import --agent    # a read-only token for Claude Code into the profile "agent"
lalabase auth status            # who the tokens belong to and what the server supports, each feature's way
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
  blue, bold frame and a bold title and stands on a ground a little
  lighter than black (256-colour 234), the others a faint frame on the
  terminal's own. While an
  agent exists (C9b), the header's first line ends with a summary of the
  agents per state, the most urgent first, each glyph in its tone with its
  count, `Agents ! 1 · ⣿ 2 · ✕ 1 · ⠹ 1 · ✓ 1`; a state without an agent is
  left out, and below 120 columns the word `Agents` too. Where the line is
  too narrow, the identity on its left is cut first, then the word goes,
  then the counts from the least urgent; the app and the profile stay
  whole. The navigation then keeps two cells before each entry for a
  glyph: an organisation shows its most urgent agent's, "All
  organisations" and the inbox and tickets entries none. Under the
  organisations stands the Agents area in a frame of its own, at most half
  the column high (all but a quarter while it has the focus), its rows
  scrolling beyond that: per agent its glyph,
  `#<number>` and the ticket's title, and under it its organisation when
  that is not the one you are in; a seen agent that ended is faint. The line
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
  and its facts: the glyph in its tone, then `Agent <state> · for <age>`;
  where the meta bar stands (below), that line moves into the bar. Free text wraps at 80
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
  right, which does not scroll: for a ticket with an agent first (C9b)
  Agent with the glyph in its tone, the state and its age
  (`⣿ waiting for you`, then `· 4 min` under it where one line is too
  short), so a low pane, which cuts the bar's end, keeps it; then Status
  in its tone, Project, People (the assignees, then the creator),
  Updated and, against a server with S6 (MCP 0.12.0, feature
  `booked_time`), Booked by you: the time you booked on the ticket, in
  hours and minutes ("2:30"), the labels faint, a long name cut with "…";
  the head then drops its line of facts and its agent's line. In a
  narrower pane they stay in one faint line under the title, the time last
  ("booked by you 2:30"). The time is read when a ticket opens, with
  `R` and after a booking, with the person's priority beside the ticket's
  own read, never for a preview; the cache (memory, else the disk) shows it at once when the
  ticket opened before, and a refusal takes it from there too. Without access to the project's time, for a ticket that is not
  there, or against an older server there is no such line.
  Comments by the token's own
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
  none shared with anything else. `/me` names the REST API's capabilities
  since the core's R0: with those of R1 (`tickets.list` and
  `tickets.show`, each with `tickets.paging`; `posts.show` for a text read
  whole) the ticket list and the ticket are read over REST (K1), without
  them through the tools; everything else stays on the tools. A failed step shows its line and `R`
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
  ticket), `PgUp`/`PgDn` (or `u`/space, for a keyboard without them)
  move by a screen, `←`/`→` move the focus, `q`
  quits with 0, Ctrl-C and SIGINT with 130. While an agent exists, `A`
  opens the agents (C9b): in three columns and in two the focus moves into
  the Agents area under the organisations, in one they are a screen of
  their own; `↑`/`↓` choose one, `Enter` shows its ticket beside the list,
  which keeps the focus (in one column it opens the ticket), `→` goes into
  the ticket and `Esc` from there back to the list, and `Esc` or `A` again
  go back to where `A` was pressed. (`a` is not this key: it is kept for "assign to
  me".) `d` removes an agent that ended or is lost, or has been starting
  for over an hour (C9c), in the agents or in its ticket's pane; `x` there
  jumps to the agent's pane or starts it again (C9c, below). The help
  line names exactly
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
  one column with the pane focused), the ticket is read as a preview: background work
  before every other, with a budget of its own of 30 starts per rolling
  minute (retries counted), held until the window rolls on when that is
  spent, canceled when the cursor moves on. While it loads for the first
  time the pane names the ticket from its row under "Loading…". `Enter` opens the ticket as the person's read and joins
  a pending preview: one request, both get the whole ticket. The pane shows
  the head, the description (cut at 8,000 characters: by the server over
  the tools, by the pane itself over REST, which sends every text whole), the
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
* **Opening a file (C10).** The client shows no images. In the section of
  attachments (`Tab`) the arrows select a file and `Enter` opens it in the
  default browser, on the server's own address for the file: the client
  builds that address from the profile's server and the ids, sends no request
  for it, and the browser uses its own login. Logged out, the browser asks
  for the login; logged in as someone else who may not read the file, it gets
  an empty 404. Nothing is downloaded. Over SSH, or without a program to
  open a browser (`open` on macOS, `xdg-open` elsewhere), a notice says so
  and nothing starts. Only the ticket's own files are listed: files of
  comments, and images inside a text, cannot be opened yet. A server that
  sends no ids shows the names alone.
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
  characters). Over REST (K1) every text comes whole, and the pane shows
  what the tools showed: 8,000 characters of a text on the page, a longer
  one ending with "Text shortened for this view", and `e` reads it whole
  as above, the description as the page again (REST has no `full_text`),
  the comment on its own route (`/api/v1/tickets/:ticket_id/posts/:id`);
  read whole, the pane shows 100,000 characters of it. REST reads a ticket
  by its id alone: one moved to another project answers from there, and is
  gone from the old project's view as the tools say it is. REST cuts no text,
  so a text read over REST that ends in the tools' marker was written so
  and shows as it is (a page the cache kept from the tools is read as
  theirs until REST's answer replaces it). The pane builds its
  whole text on every frame: a comment of a megabyte would hold every key
  for a second. Nothing is read by moving, previewing or switching
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
* **Booking time (K6).** `b` on the ticket in the focused pane opens a box
  with two fields: the duration, required, and an internal note. The booking
  is for today, in your name. `b` is offered when `/me` lists
  `time_sessions.book` and says the token may write; a read-only token, a
  token that does not say and a server without the capability have no `b`,
  in the help or behind it, and there is no way through the tools. The
  project's rights are not guessed: without them `b` shows all the same, and
  the box says the server's no.
  * **The duration goes as typed.** The server's parser is the one parser
    (`30`, `1:30` or `1.5`, as the hint says): the client reads nothing into
    the text and shows no preview. What it was read as comes back in the
    answer: the notice says "0:30 booked on #42" and is a hint for five
    seconds. An empty duration sends nothing; an empty note is not sent at
    all.
  * **One press, one booking.** Each `Enter` is one operation with its own
    idempotency key, on the ticket `b` was pressed on, wherever the selection
    goes behind the box. Only the queue's one retry (the server not reached,
    or a 429 on the first attempt) sends the same request again, under the
    same key. While a booking on a ticket is out its `b` is gone, on that
    ticket alone. `Esc` leaves the send under way, and how it ended is told
    on the status line, by the ticket's number, wherever the person is by
    then.
  * **How it ends.** Confirmed, when the answer names the ticket, an id and
    minutes above zero: the box closes, and the time on the ticket leaves the
    cache and is read again, so "Booked by you" moves; it stays blank while
    that read is out, and until `R` if it fails, since the old sum is wrong
    by now. Nothing else is read: a booking writes no activity and no
    notification. A refusal stands in the box with the server's text and
    keeps the input (`forbidden`, `read_only` and `not_found` have no text
    at the server, so the client says them); so does "the server was not
    reached" or "rate limited". The next `Enter` is a new operation with a new key.
  * **When nobody knows.** The request left and no usable answer came back,
    or an answer that does not confirm: the server may have booked. Nothing
    is sent again. The time is read as after a booking, and the box stays with
    the input and says what it found ("Not clear whether it was booked.
    Booked by you now: 1:00"). `Enter` does nothing in that box: right after
    a quick loss a second press would be a second booking. The way on is
    `Esc`, looking at the time, and `b`, which is a new action with a new key
    once the read has landed.
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
    Every press on a ticket without an agent opens a new split (since C9c
    `x` on a ticket with an agent acts on that agent instead, below).
    Elsewhere the interface gives the
    terminal to `claude` (from the `PATH`, no shell, the same arguments, no
    script) and comes back when it ends.
  * **Agents in tmux.** In iTerm with `tmux` on the `PATH`, a launch whose
    agent is tracked opens no pane: the launch script runs in a detached
    tmux session named after the agent (`tmux -L lalabase -f /dev/null
    new-session -d`, on a socket of the client's own in tmux's per-user
    directory, apart from your own tmux server; no status line, no prefix
    key, `Esc` without delay, the mouse on), and goes on when nobody looks
    at it. Beside the client stands at most one pane, the viewer: while
    the focus is on a ticket, or in the agents beside it, whose newest
    agent still runs, the viewer shows that agent's session (the first
    time the client's session is split and the pane types
    `exec tmux -L lalabase attach -t '=<agent>'`, after that the pane's
    tmux client is switched); anywhere else its client is detached and
    the pane closes. An agent whose Claude Code ends takes its session
    along. `tmux -L lalabase ls` lists the sessions that run. An agent
    started before, in a pane of its own, keeps that pane. Known limits:
    the viewer stays on the screen after `q`; a viewer closed by hand comes
    back only when another ticket asks for one; two clients at once share
    the one viewer.
    A restart resumes the conversation in a new tmux session; the old
    agent record stays until the new agent's first hook confirms it
    started, as with a direct iTerm launch.
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
    `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`,
    `Notification`, `Stop` and
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
    paths, it takes checked values and shows none: the session id,
    a subagent's `agent_id`, whether
    `SessionStart`'s source is `clear`, `resume`, `fork` or `compact`,
    whether `SessionEnd`'s reason is one of the first three, whether a
    permission request
    is the question tool `AskUserQuestion`, whether a notification is a
    `permission_prompt`, and how many background tasks run. To match an
    approval to its tool, it keeps hashes of the tool name and arguments
    and of the call id, scoped to the session and subagent; no arguments
    are written to disk. An input larger than 1 MiB or broken contributes
    no facts; unrelated or unreadable notifications change nothing.
  * **The program must stay where it is.** The hooks name the program by
    its path, links resolved. Moved or removed while a session runs,
    Claude Code shows an error line after every answer. A program path or
    an agents directory with a character no shell takes as typed (as for
    the folder above), or a ticket without its organisation, launches
    without hooks, and the status line says why.
  * **Where the files are.** `~/.cache/lalabase/cli/<profile>/a/<server>/`
    (or under `$XDG_CACHE_HOME`; `<server>` is 16 hex digits of the hash
    of the profile's server address, so a profile pointed at another
    server, a staging copy with the same ticket ids, shows none of the
    first server's agents), beside the launch directories and apart from them:
    per agent `<id>.launch.json` (the ticket's address, the pane, written
    once by the launch), `<id>.state.json` (the state, when it began, the
    latest session id, the pid and its start time, replaced whole by the
    hook under the lock `<id>.lock`) and, once you opened the agent from
    the client, `<id>.seen` (C9b, below). They are kept like the config
    (`internal/privatefs`); the hook never makes the directory. An agent
    stays until `d` or the cleanup removes it (C9c); the launch
    directory's day and the disk cache do not touch them.
  * **The states**, in order of urgency (of several agents at a ticket the
    most urgent shows):

    | State | Glyph | Tone | From |
    |---|---|---|---|
    | waiting for approval | `!` | magenta, bold | `PermissionRequest` or `Notification` with `notification_type: "permission_prompt"` |
    | waiting for you | `⣿` | magenta | `Stop`, the question tool's request, or `SessionStart` after `/clear`, `/resume` or `/branch` (no `Stop` follows them) |
    | lost | `✕` | red | the pid is gone, or now has another start time; or, written by the client, an agent that never got past starting lost its pane, or its `claude` ended here before any hook (C9c) |
    | working | braille spinner | cyan | `SessionStart` (a start, as `x`'s), `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `Stop` with a background task running |
    | starting | `⠂` | cyan, faint | no hook yet (the folder's trust dialog comes first) |
    | ended | `✓` | green, faint | `SessionEnd`, but for `/clear`, `/resume` and `/branch` |

    A compaction (`/compact`, or Claude Code's own when the context is
    full, also in the middle of a turn) changes no state.
    An unrelated parallel tool leaves an open approval or question as it
    is, including tools in the same session or subagent. `PreToolUse`
    precedes permission checks and cannot answer a prompt. Only the
    awaited tool's completion, successful or failed, clears its request; other pending requests
    remain, with approvals shown before questions.
    Sandbox network prompts arrive through a permission notification,
    normally after about six seconds without typing. Notifications name
    no tool, so their wait stays until the turn ends without background
    work, a new prompt, a session restart or the session's end. The same
    conservative rule applies when parallel calls of the same tool cannot
    be distinguished (other hooks may rewrite their arguments), an old
    record has no tool identity, or tracking exceeds
    its bounds. A compaction and a `Stop` with background work still
    running keep pending requests. These hooks are installed for new
    launches; restart sessions launched before this update to receive
    permission notifications.
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
  * **Across the organisations (C9b).** The Agents area, the glyphs at the
    organisations and the header's summary show every agent of the
    profile on this server, whichever organisation is open; agents of
    another profile or another server appear nowhere. The area orders them
    by urgency, then the oldest change first. Of an agent's files only
    its number and its state are shown: the organisation's name and the
    ticket's title come from the session (the directory, the ticket in the
    pane, the list shown, the cache). A title the session lacks is read
    once per ticket through the usual way (`get_ticket`), as idle work:
    after every waiting preview, and with at most three of the six
    background starts a minute (a title read that runs holds the
    background place while it runs); the disk cache
    is asked beside it,
    and a title found there cancels the server's read while it still
    waits;
    until it comes, and after a failed read, the row shows `#<number>`
    alone, and `R` (in the chooser or in the agents) reads the failed ones
    again. Once the session has read a ticket an agent has (its title
    read, the ticket, its full text, a list holding it), the newest answer
    it accepted decides: a title, or the ticket's removal (not found, no
    access; also of its project's list), which shows `#<number>` again.
    Answers are ordered by when their request was sent, so an older answer
    arriving late changes nothing (a project's removal included, also for
    the reads begun after a newer one), and that newest answer outranks
    what the ticket pane or a list shown still holds. Where the session
    has no answer of its own for an agent's ticket yet, the pane and the
    list shown are ranked the same way: the one a later read brought
    shows, and it counts in the order once the agent's answers come. A ticket
    of an organisation that is not in the list has no second line. The
    area and the summary follow the reading every 500 ms; they read no
    files of their own.
  * **Opening an agent.** `Enter` on an agent opens its ticket in its
    organisation's tickets (the projects of that organisation, its project
    selected, the ticket in the pane; or the list of its project, when
    that is the one shown and holds the ticket), as a notification's
    ticket opens, beside the agents' list. Leaving the list for anywhere
    but the ticket pane takes the ticket it showed along: the pane is
    empty again (or shows the list's preview of its row). An agent that ended or
    is lost is then seen: an ended one is drawn faint from then on. The
    client records it in `<id>.seen` (0600, created exclusively, never
    replaced; the hook neither reads nor writes it), so it stays seen
    after a restart. A seen file that is not a private file of your own
    in its format (broken, a link, open to others) counts as not seen, and
    the agent stays listed; a seen mark that cannot be written says so in
    the status line, and the agent is faint for that run only.
  * **Removing an agent (C9c).** `d` removes an agent that ended or is
    lost, or that has been starting for more than an hour (its folder's
    trust dialog was closed, or no pane opened, so no hook ever came),
    without asking: in the agents' list the one under the cursor, in the
    ticket pane the ticket's agent the head shows. Only the client's files
    of it go (its launch record, state, seen mark and lock); nothing that
    runs is stopped, and an agent that works or waits offers no `d`. The
    removal decides again under the agent's lock, the one its hooks and
    seen marks take:
    an agent that works again since the screen showed it stays ("it is
    running again"), and so does the cleanup below. Its launch record goes
    last, so a removal that fails part way keeps the agent listed and
    says why (the reason, without the file's path); the notice goes with
    the agent, at the next `d` or open, or with a newer launch notice
    (including a successful launch or a problem with its tracking).
  * **Cleaning up.** Every start of the interactive client cleans the
    agents' directory before its first read: an agent that ended, is lost
    or never got past starting goes 30 days after the last change of its
    files (its last hook, its end, its open), seen or not; a hook's
    temporary file, the files of an agent without its launch record and a
    launch record that fails its check go once they are an hour old. An
    agent that works or waits stays, whatever its age, and so does one
    whose launch record a newer client wrote. When a lost process went is
    not known: an agent that waited silently for 30 days and then went is
    removed at the next start without having been shown as lost. Besides
    the 30 days, at most 500 agents whose session is over stay; of more,
    those with the oldest last change go first, also before their 30
    days, so that the directory stays below what a read takes. A file
    that cannot be removed does not stop the rest. The reading visits at
    most 4096 entries of the directory (about four files per agent); a
    directory with more says so in the status line instead of showing no
    agent, and the cleanup, which visits up to 65536, brings it below at
    the next start.
* **Back to an agent (C9c).** `x` on a ticket with an agent acts on the
  agent the head shows, the most urgent at the ticket; in the agents' list
  on the one under the cursor.
  * **Jump.** An agent that works, waits or starts: iTerm selects its
    pane, found by the unique id the launch script returned, in every
    window and tab; it selects the pane's tab, then the pane, then the
    window, in this order (the window first breaks the script's
    references, the pane before its tab leaves the tab's old pane in
    front). The script is a constant run with `osascript -`, the pane's
    id, checked as a UUID, its one argument. What came of it is read from
    the error's number alone, since iTerm's texts are localised: the
    script's own 4201 says the pane is gone; a starting agent is then lost
    (below), one with a state keeps it. Without iTerm (or inside tmux, GNU
    screen or zellij), or for an agent whose pane is not known (a launch
    iTerm did not answer in time), there is no pane to select: `x` says
    so in the status line, and neither the help nor the head offers it.
    `select` does not bring iTerm to the front when another app is.
  * **Start again.** An agent that ended or is lost starts again as a new
    agent, with a launch directory of its own (a new `m` and `s`) and a
    launch record of its own (a record is never replaced): its `claude`
    resumes the session the old agent's hooks recorded last,
    `claude --mcp-config '<dir>/m' --strict-mcp-config --resume '<session id>' --settings '<dir>/s' -n 'lalabase #<n>'`,
    without the prompt, the conversation being there. That is the newest
    session id, never the launch's after a `/clear`, which would bring back
    the conversation before the clear. An agent whose hooks never recorded
    a session (lost before its first hook) has no conversation to resume:
    it starts afresh with a new session id and the prompt. Once a hook of
    the new agent came (in iTerm, seen by the next read; without iTerm,
    when `claude` ended here) the old agent's files go, decided under its
    lock as `d` decides; until then the ticket (its row, head and meta
    bar) shows the new agent, and `x` jumps to its pane, while the agents'
    area, the organisation's glyph and the header's counts still list the
    old one beside it (`x` there does nothing, `d` removes it). A read that
    failed, or that began before the new agent was recorded, does not end
    that wait. A pane alone is no proof: `claude --resume`
    finds a conversation only in the folder it ran in (a project without
    a folder entry starts in the folder `lalabase` was started in), and
    not once Claude Code cleaned its transcript up. A start that opened
    nothing leaves the old agent; one whose `claude` ended here before
    any hook, or whose pane the jump finds gone before its first hook,
    also takes the new agent's files, and the old one may be started
    again. Should the client quit before the first hook, both agents stay
    and `d` removes the old one.
    The new session's server has a new name (`lalabase_c7_<8 hex>` of the
    new launch directory): calls earlier in the conversation name the old
    one.
  * **Lost before its first hook.** A starting agent has no state and no
    pid. The client writes the state `lost` itself, under the agent's lock
    and only while no hook has written one: when the jump finds a starting
    agent's pane gone, and when `claude` ran here and ended before any hook
    (`Esc` on the folder's trust dialog). A hook that comes after replaces
    it like every state. A client from before C9c reads it as starting.
  * **On the screen.** The ticket's head names the keys that act on its
    agent, `⠹ Agent working · for 4 min · x jump to it` or
    `✓ Agent ended · for 4 min · x start again · d remove`, and so does the
    meta bar. The status line says switching to the agent, switched, its
    pane not found, no pane known, starting it again, started again, an
    agent lost with its pane, a start again that did not begin (the old
    agent stays), or that the agents could not be updated and why; what
    `x` in the agents' list did is told there and goes when the list
    closes (an answer that comes after belongs to the ticket). While
    a jump or a start again runs, a second `x` does nothing, and an agent
    whose start again is being settled is not started again twice.
* **A comment that is gone.** When the server answers a comment read whole
  with "not found" (it was deleted or made private), its cached text goes
  and is hidden at once, the ticket stays and says "Comment unavailable",
  and the ticket is read once without `post_id`: only that read may say
  the ticket is gone. "No access", or "not found" for the whole
  description, removes the ticket with every cached page and expansion.
  Without `full_text` and `post_id` there is no `e`; without the paging
  arguments no `[`/`]`/`o` in the ticket; each works without the other.
* **The dialog (M1).** The box a person writes in, for the time booked on a
  ticket first (`b`, see "Booking time"), then for a comment and a title. It
  sends nothing by itself; what a send does is its user's. To see it with no
  server, run `tools/dialog-fixture` (below).
  * **How it looks.** A box in the frame of the focused pane, in the middle
    of the screen, over whatever the panes show: the action and the ticket
    in its top border, a long title cut with "…", then the fields, each
    with its label, a faint hint under it, an error line, and the keys that
    act at the bottom. The label of the field in focus is bold, and only
    that field has a caret. Small, it has one or a few fields of one line
    and is 54 columns wide; large, it has one field of several lines and
    takes about 80 % of the screen. Below 60 columns it takes the whole
    width. The screen's last line stays free: it names `ctrl+c`, the one key
    left. A screen too low for the box gives up the empty line first, then
    the hints, then the fields furthest from the one in focus (a large box
    its hint, then its label); the keys and the field in focus stay. On a
    terminal below 24 columns or 8 rows no box shows: the notice of a small
    terminal does, with `esc` and `ctrl+c`, the only keys that act, and the
    box comes back whole when the terminal grows.
  * **Keys.** While a dialog is open every printable key is text, `q` too,
    and no key reaches the screen behind it, nor does the wheel. `Enter`
    sends a dialog of one-line fields; in a field of several lines `Enter`
    breaks the line and `Ctrl-S` sends. `Tab` goes to the next field, `Esc`
    closes the dialog, `Ctrl-C` ends the program as everywhere. `←`/`→`,
    `Home`/`End`, `Backspace` and `Delete` act on what a person sees as one
    character, however many code points it is (a combining mark, a joined
    emoji, a flag); `↑`/`↓` and `PgUp`/`PgDn` walk the lines on the screen
    in a large field, in the same column. Text wraps at spaces, and the
    field scrolls with the caret; a line of a small field wider than it
    moves sideways, a cut end marked with "…".
  * **What it holds.** Everything typed or pasted passes one filter,
    `clean.Draft`, so a field never holds what the screen would not show,
    and what is shown is what is sent. Escape sequences go whole, an
    unfinished one with the rest of the text, as on a terminal; controls,
    bidi and invisible characters go by the rules for foreign text, and a
    mark with no letter before it does not stay. A
    field of several lines keeps its line breaks, a terminal's CR and a
    file's CRLF as LF, and turns a tab into four spaces; a field of one
    line takes a run of them as one space. A paste arrives as one
    (bracketed paste), so a line break in it sends nothing; a terminal
    without bracketed paste types it, `Enter` included. Each field has a
    limit in runes: what does not fit is left out from the first grapheme
    that does not, and the error line says "Note cut at 200 characters"
    until the next key, for a value a field starts with too. A ticket's
    title in the top border and a server's text in the
    error line are foreign text and are cleaned on their way there.
  * **Sending.** The dialog keeps the ticket it was opened on: a reload that
    moves the selection behind the box does not change where a send goes.
    While a send is out the input stands still, the error line says
    "Sending…", and the keys say what is left: no `enter`, and `esc` closes
    the box, it does not cancel the send. A refusal stands in the error line
    (up to three lines, then cut), and the input stays, until the next send.
    A required field left empty stops the send before it begins and takes
    the caret; what its text means is the server's to say. A success closes
    the box. A send nobody knows the end of holds it: the input stays and
    nothing sends again, only `esc` closes it. A 401 closes it too, with the
    session: nothing of it stays on the screen, the text written so far
    included.
* **What the server supports** comes from its tool schemas, never from its
  version: without `organisation_id` only "All organisations" opens, first
  page only; without `list_notifications` the inbox is off and the counts
  are unknown ("?"); without `list_tickets` there are no tickets entries;
  without its `q` there is no search; without `get_ticket` the list opens
  nothing. Since K1 the ticket view's features also come from `/me`'s
  capabilities (see "The start"): with R1's, the list, the ticket, its
  pages and `e` work over REST whatever the tool schemas say; without
  `time_sessions.book` there is no `b`. A counter is
  unknown until a read observed it, and a newer read wins whatever order
  the answers arrive in.
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
expands, #8 a description and a newest comment of 9,000 characters,
longer than the pane shows), 3 in Handbuch, 5 in Archiv; the first organisation's rows name
Website-Relaunch's tickets (row 6 names ticket #6), the second's name
tickets the fixture does not have. It
binds to 127.0.0.1 only, accepts one synthetic token (`inboxfixture.Token`)
and refuses everything but the reads of C1 and the writes of C1f, C2 and
K6.
The ticket view is served on both ways (K1): its `/me` is the contract's,
with R1's capabilities, so the client reads the list and the tickets over
REST (`/api/v1/projects/:id/tickets`, `/api/v1/tickets/:id` with or
without a page, by number, one comment), every text whole, the same
tickets as the tools' answers; `-before-r1` (`inboxfixture.BeforeR1`)
serves a server before R1, whose `/me` lists the capabilities of API
1.2.0 and whose REST ticket answers lack the status catalogue and the
pages, and the client takes the tools. The scenarios of older servers
(`older`, `s1-only`, `s2-only`) list no capabilities at all. Writes:
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
arguments are read, and the store is one for all its writes. A booking
(K6, `POST /api/v1/tickets/:id/time_sessions`, only with R1's capabilities)
is answered in the core's order, with its texts: the token, a key seen
before, the ticket, what the input lacks, the right, the duration; so an
empty duration without the right is a 422 and a bad one a 403. It reads a
duration as the core's model does (`1,5x` is refused) and keeps the
minutes: the sum of `summarize_time_sessions` reads them afterwards. The
fixture logs attempts (`Requests`), keys (`Keys`) and the changes it
carried out (`Mutations`, with their kind) apart, for the tests. A tool a
scenario took out of `tools/list` is unknown to the fixture, as to the
server, and a scenario standing for an older server names that server's
version in `initialize`. Scenarios:
`normal`, `older` (a 0.7.0 server: no organisation filter, no paging of
comments, no expansion, no `update_ticket`, no `format`, no booked
time), `s1-only` (a 0.9.0 server: no `update_ticket`, no `format`, no
booked time), `s2-only` (a 0.10.0 server: no `format`, so a comment is
HTML to it, no booked time), `temporary` (page 2 of the first organisation's inbox and of
Website-Relaunch's tickets fails twice, `R` succeeds; so does page 2 of
ticket #6's comments), `429` (one short pause, on page 2 and on the first
attempt of every write), `401` (the session expires, also on a preview of
ticket #2 and on a write), `vanished` (the newest comments of tickets #6
and #8 are still on their page, but read whole they are "not found", on
either way), `read-only` (the token may
not write), `unknown-write` (`/me` without `read_only`), `no-mark-read`
(the tool is missing), `refusals` (marking the first organisation's row #2
is refused as archived, #3 is not found, #4 is not allowed; on
Website-Relaunch's tickets, #2 refuses a status and an assignment as
`value_not_allowed`, a title as `validation_failed` and a comment as
forbidden, #3 a status as `no_status_field`, #4 every write as forbidden,
a booking too, #5 every write as vanished, #7 an assignment as
`user_not_assignable`),
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
inbox, details, the ticket a notification names, its pages, `r`, back to
the updated inbox), the ticket's pages on both ways (over the tools with
the server's cuts and `e`, over REST with whole texts, #8 cut by the pane
and read whole), and the disk cache across two
runs on one home (`slow-reads`: the inbox and the ticket "Cached" while
the reload is out, then fresh; `temporary`: "Cached, possibly stale" and
`R`; `401`: the directory cleared and nothing refilled; an unusable
directory and a busy lock). The shipped binary does not carry the fixture
or the contract copy (`internal/sourceguard` holds that).

`tools/dialog-fixture` shows the dialog (M1) with no server at all: the box over panes of hostile text, on a ticket with a hostile
title. Every send takes a second; the first is refused with what it would have
sent, the next goes through.

```sh
go run ./tools/dialog-fixture           # the small box
go run ./tools/dialog-fixture -large    # the large one
```

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

## Release

Push a tag `v*` (a version: `v` plus digits, letters, `.`, `+`, `-`). The
release workflow runs the whole CI on it first, then builds macOS (arm64,
amd64) and Linux (amd64, arm64) without write access, and only the last job
attaches the archives with `checksums.txt` to a GitHub release.

## License

MIT, see `LICENSE`.
