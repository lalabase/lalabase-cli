package present

// Screen is everything the interface shows: display values only, never
// credentials or wire records. The root model builds one from its state in
// one place (internal/ui, presentation), the views render it and know
// nothing else.
type Screen struct {
	// Identity follows the profile in the header: whose token this is.
	Identity Inline
	// Context is the context line of two and three columns: where the
	// person is.
	Context Inline
	// Status is a notice beside the context (a rate-limit pause, an expired
	// session); empty for none.
	Status Inline
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
}

// KeyHelp is one binding of the help line.
type KeyHelp struct{ Key, Help Inline }
