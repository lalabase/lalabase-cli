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
)

type keyAction struct {
	Kind Action
	Text string
}

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
// or because the help of the state would leave the body no room for a
// title and one row.
func (m Model) small() bool {
	if m.tooSmall() {
		return true
	}
	return len(view.HelpLines(toHelp(m.bindings(false)), m.width, m.widthMethod)) > m.height-4
}

// actions are the bindings that act in the current state.
func (m Model) actions() []binding { return m.bindings(m.small()) }

// bindings are the actions of the state, with or without the room for
// them (small).
func (m Model) bindings(small bool) []binding {
	out := []binding{{Quit, uitext.QuitKey, uitext.QuitHelp}, {Interrupt, uitext.InterruptKey, uitext.InterruptHelp}}
	if small {
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
	v := a.active
	switch m.focus {
	case view.Navigation:
		if len(a.entries()) > 1 {
			out = append(out, binding{Up, uitext.UpDownKey, uitext.HighlightHelp})
		}
		if a.canOpen(a.highlight) {
			out = append(out, binding{OpenList, uitext.EnterKey, uitext.OpenHelp})
		}
		if v != nil {
			out = append(out, binding{Back, uitext.EscapeKey, uitext.InboxHelp})
		}
	case view.List:
		if v != nil && len(v.rows) > 1 {
			out = append(out, binding{Up, uitext.UpDownKey, uitext.SelectHelp}, binding{PageUp, uitext.PageUpDownKey, uitext.MoveHelp})
		}
		if v != nil && v.selected != "" {
			out = append(out, binding{OpenList, uitext.EnterKey, uitext.DetailsHelp})
		}
		// The page keys one by one: each only where its page exists.
		if v != nil && a.pagingOn() && !v.loading {
			if v.page > 1 {
				out = append(out, binding{PrevPage, uitext.PrevPageKey, uitext.PrevPageHelp})
			}
			if v.known && v.page < lastPage(v.total, v.perPage) {
				out = append(out, binding{NextPage, uitext.NextPageKey, uitext.NextPageHelp})
			}
		}
		out = append(out, binding{Back, uitext.EscapeKey, uitext.OrganisationsHelp})
	case view.Object:
		if v != nil && v.opened != "" {
			out = append(out, binding{PageUp, uitext.PageUpDownKey, uitext.ScrollHelp})
		}
		out = append(out, binding{Back, uitext.EscapeKey, uitext.CloseHelp})
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

// withoutIdleScroll drops the scroll binding of the focused column when the
// column fits its pane. The pane is measured in the geometry of the whole
// help with the binding in it: if the column fits there, it fits in the
// larger pane the help without the binding leaves, so help and dispatch
// agree either way.
func (m Model) withoutIdleScroll(out []binding) []binding {
	at := -1
	for i, b := range out {
		if b.kind == PageUp {
			at = i
		}
	}
	if at < 0 {
		return out
	}
	c, which := m.listColumn(), view.List
	if m.focus == view.Object {
		c, which = m.objectColumn(), view.Object
	}
	g := layoutWith(m.width, m.height, len(view.HelpLines(toHelp(out), m.width, m.widthMethod)))
	if view.Extent(c, g, which, m.widthMethod) > view.Capacity(c, g) {
		return out
	}
	return append(out[:at:at], out[at+1:]...)
}

// allowed is the set of actions the bindings cover.
func (m Model) allowed() map[Action]bool {
	set := map[Action]bool{}
	for _, b := range m.actions() {
		set[b.kind] = true
		if second, ok := pairs[b.kind]; ok {
			set[second] = true
		}
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
