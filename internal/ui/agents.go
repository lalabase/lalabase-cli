package ui

import (
	"errors"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// AgentWatcher reads the agents of the profile (C9a): one command per
// read, whose message is an agents.Snapshot with the same ref. The command
// gives the session one (agents.NewWatcher); tests give a fake.
type AgentWatcher interface {
	Read(ref uint64) tea.Cmd
	// See records an agent the person opened as seen (C9b); its message
	// is an agents.Marked.
	See(id string, at time.Time) tea.Cmd
	// Remove removes an agent's files if it is still removable at at, as
	// its lock is held (C9c); its message is an agents.Removed.
	Remove(id string, at time.Time) tea.Cmd
	// Settle does what follows a start or a jump (agents.Settle, C9c);
	// its message is an agents.Settled.
	Settle(after agents.After, at time.Time) tea.Cmd
}

// agentsEvery is how often the agents' files are read while there is one,
// or a launch runs: often enough for a state to show within a moment of
// its hook, and each read moves the working spinner one frame.
const agentsEvery = 500 * time.Millisecond

// agentsState is what the model knows of the agents: the newest read's
// number, whether a read is out and whether another is owed after it, the
// deadline of the next one (zero while reading sleeps), the agents of the
// newest read, the spinner's frame, and the agent the Agents area's
// cursor is on (C9b).
type agentsState struct {
	// ref numbers the reads; an answer to any but the newest is dropped.
	ref uint64
	// reading: a read is out; again: another was asked for meanwhile and
	// follows it at once (readAgents).
	reading, again bool
	// until is the deadline of the next read; zero while none is set.
	until time.Time
	// list is the newest read's agents, in the order read; empty after
	// a failed read.
	list []agents.Agent
	// frame turns the glyph of working agents, one step per read.
	frame int
	// cursor is the agent the list's cursor was put on, by its id, so it
	// follows the agent when the order changes.
	cursor string
	// seen are the agents the person opened in this run (C9b): faint at
	// once, before their file is read back.
	seen map[string]bool
	// removing are the agents d asked to remove whose answer is out, by
	// id, with their number for the notice (C9c).
	removing map[string]int
	// gone are the agents removed, by id, with the newest read issued
	// when their files went: a read up to it may have seen them before,
	// so its answer does not bring them back (receiveAgents).
	gone map[string]uint64
	// notice is what became of a removal or a seen mark, in its tone; a
	// hint goes at noticeUntil, a problem when noticeAgent leaves the
	// list, at the next d or open that starts, or at a launch.
	notice      present.Inline
	tone        present.Style
	noticeUntil time.Time
	noticeAgent string
	// tooMany: the newest read failed because the directory holds more
	// files than a read takes (agents.ErrTooManyFiles), said in the
	// status line until a read succeeds.
	tooMany bool
	// replacing are the agents x starts again, by id, until their
	// settlement answered (C9c): x does not start one twice. settling
	// are the settlements out, with what their answer's notice needs.
	replacing map[string]bool
	settling  map[agents.After]settling
}

// readAgents issues a read unless one is out or due already: one chain of
// reads at a time. A read asked for while one is out is owed: the one out
// may have seen the directory before what asked for it (a launch's
// record), so another follows it at once.
func (a *app) readAgents() tea.Cmd {
	if a.deps.Agents == nil || !a.agents.until.IsZero() {
		return nil
	}
	if a.agents.reading {
		a.agents.again = true
		return nil
	}
	a.agents.ref++
	a.agents.reading = true
	return a.deps.Agents.Read(a.agents.ref)
}

// receiveAgents takes the newest read: its agents (none when it failed:
// what cannot be read is not shown), the spinner one frame on while one
// works, and the next read in agentsEvery by the model's clock, while
// there is an agent, a launch runs or the read failed (the agents come
// back once the directory reads again). Only a read that found none
// lets reading sleep until the next launch.
func (a *app) receiveAgents(s agents.Snapshot) tea.Cmd {
	if !a.agents.reading || s.Ref != a.agents.ref {
		return nil
	}
	a.agents.reading = false
	a.agents.tooMany = errors.Is(s.Err, agents.ErrTooManyFiles)
	a.agents.list = withoutGone(s.Agents, a.agents.gone, s.Ref)
	if s.Err != nil {
		a.agents.list = nil
	}
	if id := a.agents.noticeAgent; id != "" && s.Err == nil && !slices.ContainsFunc(a.agents.list, func(ag agents.Agent) bool { return ag.ID == id }) {
		// A problem told of an agent goes with it: nothing could clear
		// it once the agent is no longer listed. A failed read lists no
		// agent but says nothing of this one, and keeps the problem.
		a.clearAgentsNotice()
	}
	for _, ag := range a.agents.list {
		if ag.State == agents.Working {
			a.agents.frame++
			break
		}
	}
	// A new agent's title, if the session lacks it, is read once.
	titles := a.requestTitles()
	if a.agents.again {
		a.agents.again = false
		return tea.Batch(titles, a.readAgents())
	}
	if (s.Err == nil && len(a.agents.list) == 0 && !a.launch.busy) || a.deps.Events == nil {
		return titles
	}
	now := a.now()
	a.agents.until = now.Add(agentsEvery)
	return tea.Batch(titles, a.deps.Events.Expiry(a.agents.until, now))
}

// agentsDue is the deadline of the next read: it reads, if it is still
// the one set.
func (a *app) agentsDue(until time.Time) tea.Cmd {
	if a.agents.until.IsZero() || !until.Equal(a.agents.until) {
		return nil
	}
	a.agents.until = time.Time{}
	return a.readAgents()
}

// agentAt is the most urgent agent at a ticket; ok is false when it has
// none.
func (a *app) agentAt(ticket string) (agents.Agent, bool) {
	return a.mostUrgent(func(ag agents.Agent) bool { return ag.Ticket == ticket })
}

// mostUrgent is the most urgent of the agents that match, of two equally
// urgent the first read; ok is false when none matches.
func (a *app) mostUrgent(match func(agents.Agent) bool) (agents.Agent, bool) {
	var best agents.Agent
	found := false
	for _, ag := range a.agents.list {
		if !match(ag) {
			continue
		}
		if !found || ag.State.Urgent(best.State) {
			best, found = ag, true
		}
	}
	return best, found
}

// agentsInOrder are the agents in the order of the area (C9b): the most
// urgent first, of one state the oldest change first, else as read.
func (a *app) agentsInOrder() []agents.Agent {
	out := slices.Clone(a.agents.list)
	slices.SortStableFunc(out, func(x, y agents.Agent) int {
		switch {
		case x.State.Urgent(y.State):
			return -1
		case y.State.Urgent(x.State):
			return 1
		}
		return x.Since.Compare(y.Since)
	})
	return out
}

// agentSummary is the header's summary (C9b): per state, the most urgent
// first, its glyph in its tone and its number of agents; a state without
// one is left out.
func (a *app) agentSummary() []present.AgentCount {
	var counts [agents.Ended + 1]int
	for _, ag := range a.agents.list {
		if ag.State <= agents.Ended {
			counts[ag.State]++
		}
	}
	var out []present.AgentCount
	for s, n := range counts {
		if n > 0 {
			l := a.look(agents.State(s))
			out = append(out, present.AgentCount{Glyph: present.NewInline(l.glyph), Style: l.style, Count: n})
		}
	}
	return out
}

// spinner is the working glyph, one frame per read: braille, one cell
// each.
var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// agentLook is how a state is drawn wherever an agent shows (the area,
// the ticket rows, the header's counts, the ticket's head, the meta bar):
// its glyph, one cell; its tone; its word. The glyph's shape and the word
// carry the state where no colour is drawn (NO_COLOR, the cursor bar);
// the tone only weighs it.
type agentLook struct {
	glyph string
	style present.Style
	word  uitext.ID
}

// agentLooks are the states' looks, one table so that no state can have a
// glyph without a tone or a word. Working's glyph turns: look takes it from
// the spinner.
var agentLooks = map[agents.State]agentLook{
	agents.WaitingForApproval: {"!", present.AgentApproval, uitext.AgentApproval},
	agents.WaitingForYou:      {"⣿", present.AgentWaiting, uitext.AgentWaiting},
	agents.Lost:               {"✕", present.AgentLost, uitext.AgentLost},
	agents.Working:            {"", present.AgentWorking, uitext.AgentWorking},
	agents.Starting:           {"⠂", present.AgentStarting, uitext.AgentStarting},
	agents.Ended:              {"✓", present.AgentEnded, uitext.AgentEnded},
}

// look is a state's look; working's glyph is the spinner's frame of this
// read.
func (a *app) look(s agents.State) agentLook {
	l := agentLooks[s]
	if s == agents.Working {
		l.glyph = spinner[a.agents.frame%len(spinner)]
	}
	return l
}

// markAgent puts the glyph of the most urgent agent at the ticket into the
// row's column, which every ticket list reserves.
func (a *app) markAgent(row *present.Row, ticket string) {
	row.Marked = true
	if ag, ok := a.agentAt(ticket); ok {
		l := a.look(ag.State)
		row.Mark, row.MarkStyle = present.NewInline(l.glyph), l.style
	}
}

// agentHead is the line of the ticket's head for its most urgent agent:
// its glyph in its tone, then its word and how long its state has lasted
// by the model's clock, then the keys that act on it (C9c). ok is false
// when the ticket has no agent.
func (a *app) agentHead(ticket string) (glyph string, style present.Style, text string, ok bool) {
	ag, ok := a.agentAt(ticket)
	if !ok {
		return "", present.Plain, "", false
	}
	l := a.look(ag.State)
	line := uitext.Fill(uitext.AgentLine, uitext.Inline(l.word).String(), age(a.now().Sub(ag.Since))).String()
	for _, hint := range a.agentHints(ag) {
		line += " " + hint
	}
	return l.glyph, l.style, line, true
}

// agentHints are the keys that act on the ticket's agent, each after a
// "· " (C9c): x jumps to the pane of one that runs, waits or starts, when
// there is a pane to select; x starts one whose session is over again,
// and d removes it. A key that does nothing here is not named.
func (a *app) agentHints(ag agents.Agent) []string {
	var hints []uitext.ID
	if a.deps.Launcher != nil {
		switch a.xFor(ag) {
		case xJump:
			hints = append(hints, uitext.AgentHintJump)
		case xAgain:
			hints = append(hints, uitext.AgentHintAgain)
		}
	}
	if a.deps.Agents != nil && (ag.State == agents.Ended || ag.State == agents.Lost) {
		hints = append(hints, uitext.AgentHintRemove)
	}
	out := make([]string, 0, len(hints))
	for _, id := range hints {
		out = append(out, uitext.Fill(uitext.AgentMeta, uitext.Inline(id).String()).String())
	}
	return out
}

// agentMeta are the meta bar's first lines for the ticket's most urgent
// agent (C9b): a faint label, then its glyph in its tone, its word, how
// long its state has lasted and the keys that act on it (C9c), then an
// empty line before the facts. A value
// too long for the bar is broken at its spaces, the lines after the first
// under the word, so neither the state nor its age is cut. None without
// an agent.
func (a *app) agentMeta(ticket string) []present.MetaLine {
	ag, ok := a.agentAt(ticket)
	if !ok {
		return nil
	}
	l := a.look(ag.State)
	out := []present.MetaLine{{Text: uitext.Inline(uitext.BarAgent), Style: present.Faint}}
	for i, line := range barLines(uitext.Inline(l.word).String(), age(a.now().Sub(ag.Since)), a.agentHints(ag), view.MetaRoom-markCells) {
		if i == 0 {
			out = append(out, present.MetaLine{Lead: present.NewInline(l.glyph), LeadStyle: l.style, Text: present.NewInline(line)})
			continue
		}
		out = append(out, present.MetaLine{Text: present.NewInline(strings.Repeat(" ", markCells) + line)})
	}
	return append(out, present.MetaLine{})
}

// markCells is the room of a glyph and the space after it.
const markCells = 2

// barLines breaks the agent's word and age at their spaces into lines of
// at most width cells (the client's own ASCII text); the "· " before the
// age stays with it, and each key's hint stays whole (C9c). A word wider
// than the line stands alone and is cut by the view.
func barLines(word, age string, hints []string, width int) []string {
	words := append(strings.Fields(word), uitext.Fill(uitext.AgentMeta, age).String())
	words = append(words, hints...)
	var out []string
	line := ""
	for _, w := range words {
		if line != "" && len(line)+1+len(w) > width {
			out = append(out, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	return append(out, line)
}

// age is how long a state has lasted, in its largest whole unit; a time
// ahead of the clock is no time at all.
func age(d time.Duration) string {
	d = max(0, d)
	switch {
	case d < time.Minute:
		return uitext.Fill(uitext.AgeSeconds, int(d/time.Second)).String()
	case d < time.Hour:
		return uitext.Fill(uitext.AgeMinutes, int(d/time.Minute)).String()
	case d < 24*time.Hour:
		return uitext.Fill(uitext.AgeHours, int(d/time.Hour)).String()
	}
	return uitext.Fill(uitext.AgeDays, int(d/(24*time.Hour))).String()
}

// withoutGone is the agents of the read ref, less those removed when
// that read may already have been out (gone); an entry of gone a later
// read passed is forgotten, since that read saw the directory after.
func withoutGone(list []agents.Agent, gone map[string]uint64, ref uint64) []agents.Agent {
	for id, upTo := range gone {
		if ref > upTo {
			delete(gone, id)
		}
	}
	if len(gone) == 0 {
		return list
	}
	out := make([]agents.Agent, 0, len(list))
	for _, ag := range list {
		if _, ok := gone[ag.ID]; !ok {
			out = append(out, ag)
		}
	}
	return out
}
