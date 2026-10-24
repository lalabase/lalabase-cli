package present

// Screen is everything the interface shows: display values only, never
// credentials or wire records. The root model builds one from its state in
// one place (internal/ui, presentation), the views render it and know
// nothing else.
type Screen struct {
	// Identity follows the profile in the header: whose token this is.
	Identity Inline
	// Context is the path in the second line of two and three columns:
	// where the person is.
	Context Inline
	// Status is a notice of the second line; empty for none. A problem or
	// a wait (a rate-limit pause, an expired session) stands beside the
	// path; a plain one is a short hint (what x did), which stands in the
	// path's place.
	Status Inline
	// StatusStyle is the notice's tone: Plain (a hint), NoticeProblem or
	// NoticeWaiting.
	StatusStyle Style
	// Crumbs are the names of the three panes, for the breadcrumb of one
	// column.
	Crumbs  [3]Inline
	Chooser Column
	List    Column
	Object  Column
	// Help are the bindings that act right now, in the order they are shown.
	Help []KeyHelp
}

// Column is one of the three panes.
type Column struct {
	Title Inline
	// Notice is one line under the title (loading, an error, "Cached");
	// empty for none.
	Notice Inline
	// Rows is a list, one entry per row; nil for free text.
	Rows []Row
	// Cursor is the row the person is on; -1 for none.
	Cursor int
	// Text is free text, wrapped by the view, when Rows is nil.
	Text Body
	// Styles says how each line of Text is drawn, one per line; a line
	// without one is plain.
	Styles []LineStyle
	// Offset is the first visible row, or line of the text. The view clamps
	// it to what fits and keeps the cursor in sight.
	Offset int
}

// Row is one entry of a list: a first line with an optional value at its
// end (a count), an optional second line, and whether the entry is the one
// in use (the active organisation).
type Row struct {
	Primary   Inline
	Trailing  Inline
	Secondary Inline
	Active    bool
	// Quiet: the entry holds nothing to act on (an inbox without unread
	// notifications) and is drawn faint.
	Quiet bool
	// TrailingStyle is how the value at the end is drawn when not as the
	// row: bold when it asks for attention (unread notifications), a
	// status in the tone of its meaning.
	TrailingStyle Style
}

// Style is how part of a line is drawn. Text stays readable on a terminal
// without attributes: a style only weighs it, it never carries meaning
// alone.
type Style uint8

const (
	Plain Style = iota
	Strong
	Faint
	// The tones of a ticket's status, by what it means in its project
	// (the server's status functions): open, being worked on, done. The
	// status's name stands in the tone, so the colour never carries the
	// meaning alone; a status waiting in the backlog is Faint.
	StatusOpen
	StatusActive
	StatusDone
	// The tones of the status notice beside the context, by the state it
	// reports: something did not work (a launch, the session, the disk
	// cache), or the client waits (a launch running, a rate-limit pause).
	// The notice's text says what; the tone only makes it seen.
	NoticeProblem
	NoticeWaiting
)

// LineStyle is how one line of a column's text is drawn: its first Lead
// bytes in LeadStyle, the rest in RestStyle, every screen line it wraps
// into indented by Indent cells; a heading (Rule) ends in a faint rule to
// the reading width.
type LineStyle struct {
	Lead      int
	LeadStyle Style
	RestStyle Style
	Indent    int
	Rule      bool
}

// KeyHelp is one binding of the help line.
type KeyHelp struct{ Key, Help Inline }
