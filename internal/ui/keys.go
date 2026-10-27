package ui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

type Mode uint8

const (
	Navigate Mode = iota
	TextEntry
)

type Action uint8

const (
	Ignore Action = iota
	Quit
	Interrupt
	Left
	Right
	OpenList
	Back
	InsertText
	Submit
	Cancel
	Up
	Down
	PageUp
	PageDown
	PrevPage
	NextPage
	Refresh
	Search
	DeleteBack
	// C1d: the ticket's sections, order and expansion.
	NextArea
	Order
	Expand
	// C1f: mark a notification as read.
	MarkRead
	// C7: Claude Code beside the ticket.
	Launch
	// C9b: the agents, and back from them.
	OpenAgents
)

type keyAction struct {
	Kind Action
	Text string
}

// routeKey names what a key does in a mode. In navigation every key is one
// action or nothing; in text entry every printable key is text, Enter
// sends, Esc cancels, Backspace deletes, and Ctrl-C interrupts as
// everywhere. A key never does both.
func routeKey(mode Mode, key tea.KeyPressMsg) keyAction {
	name := key.String()
	if name == "ctrl+c" {
		return keyAction{Kind: Interrupt}
	}
	if mode == TextEntry {
		switch name {
		case "enter":
			return keyAction{Kind: Submit}
		case "esc":
			return keyAction{Kind: Cancel}
		case "backspace":
			return keyAction{Kind: DeleteBack}
		}
		text := key.Text
		if text == "" && key.Code <= unicode.MaxRune && unicode.IsPrint(key.Code) && key.Mod & ^tea.ModShift == 0 {
			text = string(key.Code)
		}
		if text != "" && key.Mod & ^tea.ModShift == 0 {
			for _, r := range text {
				if !unicode.IsPrint(r) {
					return keyAction{}
				}
			}
			return keyAction{Kind: InsertText, Text: text}
		}
		return keyAction{}
	}
	switch name {
	case "q":
		return keyAction{Kind: Quit}
	case "left":
		return keyAction{Kind: Left}
	case "right":
		return keyAction{Kind: Right}
	case "enter":
		return keyAction{Kind: OpenList}
	case "esc":
		return keyAction{Kind: Back}
	case "up":
		return keyAction{Kind: Up}
	case "down":
		return keyAction{Kind: Down}
	case "pgup":
		return keyAction{Kind: PageUp}
	case "pgdown":
		return keyAction{Kind: PageDown}
	case "[":
		return keyAction{Kind: PrevPage}
	case "]":
		return keyAction{Kind: NextPage}
	case "R":
		return keyAction{Kind: Refresh}
	case "/":
		return keyAction{Kind: Search}
	case "tab":
		return keyAction{Kind: NextArea}
	case "o":
		return keyAction{Kind: Order}
	case "e":
		return keyAction{Kind: Expand}
	case "r":
		return keyAction{Kind: MarkRead}
	case "x":
		return keyAction{Kind: Launch}
	case "A":
		return keyAction{Kind: OpenAgents}
	}
	return keyAction{}
}

// binding is one action that is active right now: what the key does, and
// how the help names it. The dispatch and the help read the same list.
type binding struct {
	kind      Action
	key, help uitext.ID
}

// pairs says which second key a binding's help covers: "up/down" is one
// entry of the help, two keys of the dispatch. A pair is offered when there
// is somewhere to go in at least one direction; at an end, the other key
// does nothing, which is what a cursor key means there (a decision, not a
// gap: keys offered one by one would change the help on every move).
var pairs = map[Action]Action{Up: Down, PageUp: PageDown, Left: Right}

// tooSmall is the terminal below the size anything can be shown in.
func (m Model) tooSmall() bool { return m.width < 24 || m.height < 8 }

// small is the terminal too small for anything but leaving: by its size,
// or because the help of the state would leave the focused column no room
// for one row (its title and a notice come first).
func (m Model) small() bool {
	if m.tooSmall() {
		return true
	}
	g := layoutWith(m.width, m.height, len(view.HelpLines(toHelp(m.bindings(false)), m.width, m.widthMethod)), m.focus)
	return m.focusedCapacity(g) < 1
}

// focusedCapacity is the number of rows (or lines) the focused column
// shows at once in g: the Agents area, in three columns, has the part of
// the navigation it takes, the chooser the rest (C9b).
func (m Model) focusedCapacity(g view.Geometry) int {
	c := m.focusedColumn()
	switch {
	case m.focus == view.Agents && g.Columns == 3:
		return view.AgentsCapacity(m.chooserColumn(), c, g.BodyHeight)
	case m.focus == view.Navigation && m.app != nil:
		// The navigation shares its pane with the Agents area, in every
		// layout it shows in.
		return view.ChooserCapacity(c, m.agentsColumn(), g.BodyHeight)
	}
	return view.Capacity(c, g)
}

// focusedColumn is the column the focus is on, of the session or of the
// shell.
func (m Model) focusedColumn() present.Column {
	if m.app == nil {
		s := presentation(m.shell[0], m.shell[1], m.shell[2])
		return [...]present.Column{view.Navigation: s.Chooser, view.List: s.List, view.Object: s.Object}[m.focus]
	}
	switch m.focus {
	case view.List:
		return m.listColumn()
	case view.Object:
		return m.objectColumn()
	case view.Agents:
		return m.agentsColumn()
	}
	return m.chooserColumn()
}

// actions are the bindings that act in the current state.
func (m Model) actions() []binding { return m.bindings(m.small()) }

// bindings are the actions of the state, with or without the room for
// them (small).
func (m Model) bindings(small bool) []binding {
	if m.mode == TextEntry {
		// The search line: q is text here, so quitting is not offered;
		// deleting only while there is something to delete.
		out := []binding{{Cancel, uitext.EscapeKey, uitext.CancelHelp}, {Interrupt, uitext.InterruptKey, uitext.InterruptHelp}}
		if small {
			return out
		}
		out = append([]binding{{Submit, uitext.EnterKey, uitext.SearchHelp}}, out...)
		if a := m.app; a != nil && a.tickets != nil && a.tickets.entry != "" {
			out = append(out[:2:2], binding{DeleteBack, uitext.BackspaceKey, uitext.DeleteHelp}, out[2])
		}
		return out
	}
	out := []binding{{Quit, uitext.QuitKey, uitext.QuitHelp}, {Interrupt, uitext.InterruptKey, uitext.InterruptHelp}}
	if small {
		if m.focus == view.Agents {
			// A resize left the agents no room: the way back stays (C9b).
			out = append(out, binding{Back, uitext.EscapeKey, uitext.BackHelp})
		}
		return out
	}
	a := m.app
	if a == nil {
		out = append(out, binding{Left, uitext.FocusKey, uitext.FocusHelp}, binding{Back, uitext.EscapeKey, uitext.EscapeHelp})
		if m.focus == view.Navigation {
			out = append(out, binding{OpenList, uitext.EnterKey, uitext.EnterHelp})
		}
		return out
	}
	if a.expired {
		return out
	}
	if a.steps[stepMe].state != ready {
		if a.steps[stepMe].state == failed {
			out = append(out, binding{Refresh, uitext.RefreshKey, uitext.RetryHelp})
		}
		return out
	}
	if m.focus == view.Agents {
		// The agents' list (C9b): its own keys; esc and a second A go
		// back to where it was opened. Returning here also ends the
		// recursion below: agentsFit asks small of the model with the
		// agents in focus, small asks bindings, and those, here, do not
		// ask agentsFit again.
		return append(out, m.agentsBindings()...)
	}
	switch m.focus {
	case view.Navigation:
		out = append(out, m.chooserBindings()...)
	case view.List:
		out = append(out, m.listBindings()...)
	case view.Object:
		out = append(out, m.objectBindings()...)
	}
	if len(a.agents.list) > 0 && m.agentsFit() {
		out = append(out, binding{OpenAgents, uitext.AgentsKey, uitext.AgentsHelp})
	}
	if m.canRefresh() {
		help := uitext.RefreshHelp
		if a.anyFailed() {
			help = uitext.RetryHelp
		}
		out = append(out, binding{Refresh, uitext.RefreshKey, help})
	}
	return m.withoutIdleScroll(append(out, binding{Left, uitext.FocusKey, uitext.FocusHelp}))
}

// shown: something is open in the list pane (an inbox or the tickets
// section), so Esc from the chooser has somewhere to go.
func (a *app) shown() bool { return a.active != nil || a.projects != nil }

func (m Model) chooserBindings() []binding {
	a := m.app
	var out []binding
	if len(a.entries()) > 1 {
		out = append(out, binding{Up, uitext.UpDownKey, uitext.HighlightHelp})
	}
	if a.canOpen(a.highlight, a.highlightSection) {
		help := uitext.OpenHelp
		if a.highlightSection == sectionTickets {
			help = uitext.OpenTicketsHelp
		}
		out = append(out, binding{OpenList, uitext.EnterKey, help})
	}
	if a.shown() {
		help := uitext.InboxHelp
		if a.projects != nil {
			help = uitext.TicketsHelp
		}
		out = append(out, binding{Back, uitext.EscapeKey, help})
	}
	return out
}

func (m Model) listBindings() []binding {
	a := m.app
	var out []binding
	switch {
	case a.active != nil:
		v := a.active
		if len(v.rows) > 1 {
			// The page keys move the selection by a page, to the ends at
			// the latest: they act whenever there is a second row, however
			// many rows the pane shows (a pane's capacity is not monotone
			// in the help's height, so nothing here reads the geometry).
			out = append(out, binding{Up, uitext.UpDownKey, uitext.SelectHelp}, binding{PageUp, uitext.PageUpDownKey, uitext.MoveHelp})
		}
		if v.selected != "" {
			out = append(out, binding{OpenList, uitext.EnterKey, uitext.DetailsHelp})
		}
		if _, ok := m.markTarget(); ok {
			out = append(out, binding{MarkRead, uitext.MarkReadKey, uitext.MarkReadHelp})
		}
		// The page keys one by one: each only where its page exists.
		if a.pagingOn() && !v.loading {
			if v.page > 1 {
				out = append(out, binding{PrevPage, uitext.PrevPageKey, uitext.PrevPageHelp})
			}
			if v.known && v.page < lastPage(v.total, v.perPage) {
				out = append(out, binding{NextPage, uitext.NextPageKey, uitext.NextPageHelp})
			}
		}
		out = append(out, binding{Back, uitext.EscapeKey, uitext.OrganisationsHelp})
	case a.tickets != nil:
		v := a.tickets
		if len(v.rows) > 1 {
			out = append(out, binding{Up, uitext.UpDownKey, uitext.SelectHelp}, binding{PageUp, uitext.PageUpDownKey, uitext.MoveHelp})
		}
		if v.selected != "" && a.ticketOn() {
			out = append(out, binding{OpenList, uitext.EnterKey, uitext.OpenTicketHelp})
		}
		if !v.loading {
			if v.page > 1 {
				out = append(out, binding{PrevPage, uitext.PrevPageKey, uitext.PrevPageHelp})
			}
			if v.known && v.page < lastPage(v.total, v.perPage) {
				out = append(out, binding{NextPage, uitext.NextPageKey, uitext.NextPageHelp})
			}
		}
		if a.searchOn() {
			out = append(out, binding{Search, uitext.SearchKey, uitext.SearchHelp})
		}
		out = append(out, binding{Back, uitext.EscapeKey, uitext.ProjectsHelp})
	case a.projects != nil:
		v := a.projects
		if len(v.rows) > 1 {
			out = append(out, binding{Up, uitext.UpDownKey, uitext.SelectHelp}, binding{PageUp, uitext.PageUpDownKey, uitext.MoveHelp})
		}
		if v.selected != "" {
			out = append(out, binding{OpenList, uitext.EnterKey, uitext.OpenTicketsHelp})
		}
		out = append(out, binding{Back, uitext.EscapeKey, uitext.OrganisationsHelp})
	default:
		out = append(out, binding{Back, uitext.EscapeKey, uitext.OrganisationsHelp})
	}
	return out
}

func (m Model) objectBindings() []binding {
	a := m.app
	var out []binding
	switch {
	case a.origin != nil:
		// The ticket opened from a notification (C1f): the ticket's keys,
		// r for its notification, Esc back to the details.
		out = append(out, m.ticketBindings()...)
		if _, ok := m.markTarget(); ok {
			out = append(out, binding{MarkRead, uitext.MarkReadKey, uitext.MarkReadHelp})
		}
		help := uitext.NotificationHelp
		if a.detail != nil && a.detail.expansion != nil {
			help = uitext.CollapseHelp
		}
		out = append(out, binding{Back, uitext.EscapeKey, help})
	case a.active != nil:
		if a.active.opened != "" {
			out = append(out, binding{PageUp, uitext.PageUpDownKey, uitext.ScrollHelp})
		}
		if _, ok := a.linkedTicket(); ok {
			out = append(out, binding{Order, uitext.OrderKey, uitext.OpenLinkedTicketHelp})
		}
		if _, ok := m.markTarget(); ok {
			out = append(out, binding{MarkRead, uitext.MarkReadKey, uitext.MarkReadHelp})
		}
		out = append(out, binding{Back, uitext.EscapeKey, uitext.CloseHelp})
	case a.tickets != nil:
		out = append(out, m.ticketBindings()...)
		help := uitext.ListHelp
		switch {
		case a.detail != nil && a.detail.expansion != nil:
			help = uitext.CollapseHelp
		case a.tickets.opened != "":
			help = uitext.BackHelp
		}
		out = append(out, binding{Back, uitext.EscapeKey, help})
	case a.agentTicket():
		// The ticket an agent opened (C9b): its keys, Esc to the projects.
		out = append(out, m.ticketBindings()...)
		help := uitext.ListHelp
		if a.detail.expansion != nil {
			help = uitext.CollapseHelp
		}
		out = append(out, binding{Back, uitext.EscapeKey, help})
	default:
		out = append(out, binding{Back, uitext.EscapeKey, uitext.ListHelp})
	}
	return out
}

// ticketBindings are the keys of the ticket in the pane (C1d): the
// sections, the comment selection, the pages and the order of the active
// section, the expansion, the scroll. Each is offered only where it acts.
func (m Model) ticketBindings() []binding {
	d := m.app.detail
	if d == nil || !d.known {
		return nil
	}
	out := []binding{{NextArea, uitext.TabKey, uitext.NextAreaHelp}}
	if d.area == areaComments && len(d.record.Posts) > 1 && !d.commentsLoading() {
		out = append(out, binding{Up, uitext.UpDownKey, uitext.CommentHelp})
	}
	if m.canTurn(-1) {
		out = append(out, binding{PrevPage, uitext.PrevPageKey, uitext.PrevPageHelp})
	}
	if m.canTurn(1) {
		out = append(out, binding{NextPage, uitext.NextPageKey, uitext.NextPageHelp})
	}
	if m.canOrder() {
		help := uitext.OldestFirst
		if !d.key.Newest {
			help = uitext.NewestFirst
		}
		out = append(out, binding{Order, uitext.OrderKey, help})
	}
	if _, ok := m.expandTarget(); ok {
		out = append(out, binding{Expand, uitext.ExpandKey, uitext.ExpandHelp})
	}
	if _, ok := m.launchTarget(); ok {
		out = append(out, binding{Launch, uitext.LaunchKey, uitext.LaunchHelp})
	}
	return append(out, binding{PageUp, uitext.PageUpDownKey, uitext.ScrollHelp})
}

// withoutIdleScroll drops the scroll binding of the details when their text
// fits the pane. The pane is measured in the geometry of the help without
// the binding: if the text overflows there, it overflows the smaller pane
// the binding's own line leaves (a text's lines depend on the width alone),
// and if it fits there, that is the pane the person gets. Help and dispatch
// agree either way.
func (m Model) withoutIdleScroll(out []binding) []binding {
	if m.focus != view.Object {
		return out
	}
	at := -1
	for i, b := range out {
		if b.kind == PageUp {
			at = i
		}
	}
	if at < 0 {
		return out
	}
	without := append(out[:at:at], out[at+1:]...)
	g := m.withNotice(layoutWith(m.width, m.height, len(view.HelpLines(toHelp(without), m.width, m.widthMethod)), m.focus))
	c := m.objectColumn()
	if view.Extent(c, g, view.Object, m.widthMethod) > view.Capacity(c, g) {
		return out
	}
	return without
}

// allowed is the set of actions the bindings cover. While the search line
// is open, and there is room for it, every printable key is text
// (InsertText), which no binding names.
func (m Model) allowed() map[Action]bool {
	set := map[Action]bool{}
	for _, b := range m.actions() {
		set[b.kind] = true
		if second, ok := pairs[b.kind]; ok {
			set[second] = true
		}
	}
	if m.mode == TextEntry && !m.small() {
		set[InsertText] = true
	}
	if set[Back] && m.focus == view.Agents {
		// A second A closes the agents as esc does; the help names esc.
		set[OpenAgents] = true
	}
	return set
}

func toHelp(bindings []binding) []present.KeyHelp {
	out := make([]present.KeyHelp, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, present.KeyHelp{Key: uitext.Inline(b.key), Help: uitext.Inline(b.help)})
	}
	return out
}

// help is the help line's content: the bindings, in order.
func (m Model) help() []present.KeyHelp { return toHelp(m.actions()) }
