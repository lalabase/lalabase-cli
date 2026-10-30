package ui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The ticket page by page (C1d): Tab moves between its three sections, [
// and ] turn the page of the comments or of the attachments, o switches
// the order of the comments, e reads the description or the selected
// comment whole. Every one of them is the person's action; nothing is read
// by moving, previewing or switching sections.

// ticketArea is a section of the ticket in the object pane.
type ticketArea uint8

const (
	areaDescription ticketArea = iota
	areaComments
	areaAttachments
	ticketAreas
)

// anchor is what the pane keeps in sight: nothing (the person scrolled),
// the heading of the active section, the selected comment or the selected
// file. The line is found when the pane is built, so a resize keeps the
// place.
type anchor uint8

const (
	anchorNone anchor = iota
	anchorArea
	anchorPost
	anchorFile
)

// expansion is the one text of the ticket read whole: the description
// (post "") or one comment. It is a read of its own, never a page: its
// answer fills the text and nothing else of the ticket.
type expansion struct {
	key  cache.Key
	post string
	text string
	// uncut: the text came over REST, which cuts none (detailView.uncut).
	uncut bool
	status
	// unavailable: the server said the comment is missing or hidden
	// (api.CommentNotFound); its text is not shown any more.
	unavailable bool
}

// pagedKey: the key reads a page of comments and of attachments (a server
// with ticket_posts_paged), not the plain first answer.
func pagedKey(k cache.Key) bool { return k.PostsPage > 0 }

// commentsLoading and attachmentsLoading: the record shown answers another
// page of that section than the one asked for, which is still on its way.
func (d *detailView) commentsLoading() bool {
	return d.shown.PostsPage != d.key.PostsPage || d.shown.Newest != d.key.Newest
}

func (d *detailView) attachmentsLoading() bool {
	return d.shown.AttachmentsPage != d.key.AttachmentsPage
}

// selectedPost is the selected comment of the record shown, nil for none.
func (d *detailView) selectedPost() *api.Post {
	for i := range d.record.Posts {
		if d.record.Posts[i].ID == d.post {
			return &d.record.Posts[i]
		}
	}
	return nil
}

// startAtHead puts a ticket that opens anew in the pane at its head: the
// description in view, no comment selected, no anchor to follow. A ticket
// opened from a notification and one opened from an agent start so; Enter
// on a ticket row resets the position alone (openTicket).
func (d *detailView) startAtHead() {
	d.offset = 0
	d.follow = anchorNone
	d.area = areaDescription
	d.post = ""
	d.postFile = ""
	d.file = ""
}

// settle follows a new record: the selected comment is kept by its id or
// moves to the first one, a file no longer on the page gives way to its
// comment; an expanded comment no longer on the page closes, a missing
// one's marker too (nothing stays open that the pane does not show; review
// round 2).
func (a *app) settle(d *detailView) {
	posts := d.record.Posts
	if !slices.ContainsFunc(posts, func(p api.Post) bool { return p.ID == d.post }) {
		d.post, d.postFile = "", ""
		if len(posts) > 0 {
			d.post = posts[0].ID
		}
	}
	if _, ok := d.commentFile(); !ok {
		d.postFile = ""
	}
	if e := d.expansion; e != nil && e.post != "" &&
		!slices.ContainsFunc(posts, func(p api.Post) bool { return p.ID == e.post }) {
		a.closeExpansion()
	}
}

// closeExpansion closes the expansion, if any, and refuses every answer
// still out for it: a late one cannot open it again.
func (a *app) closeExpansion() {
	a.cancelArea(gateway.Expansion)
	if d := a.detail; d != nil {
		d.expansion = nil
	}
}

// --- sections and selection --------------------------------------------------

// nextArea is Tab: the next section, its heading brought into sight.
func (m Model) nextArea() Model {
	d := m.app.detail
	d.area = (d.area + 1) % ticketAreas
	d.follow = anchorArea
	return m
}

// stop is a place of the walk through the comments (C12): a comment, file
// "", or one of its files.
type stop struct{ post, file string }

// stops are the places of the walk on the page shown, in the pane's order:
// a comment, the files of it that can be opened, the next comment. Without
// files they are the comments.
func (d *detailView) stops() []stop {
	var out []stop
	for _, p := range d.record.Posts {
		out = append(out, stop{post: p.ID})
		for _, f := range d.commentFiles(p) {
			out = append(out, stop{p.ID, *f.ID})
		}
	}
	return out
}

// moveComment walks the selection through the comments of the page shown
// and their files, and keeps it in sight.
func (m Model) moveComment(delta int) Model {
	d := m.app.detail
	stops := d.stops()
	if len(stops) == 0 {
		return m
	}
	place := stop{post: d.post}
	if f, ok := d.commentFile(); ok {
		place.file = *f.ID
	}
	at := max(0, slices.Index(stops, place))
	next := stops[max(0, min(len(stops)-1, at+delta))]
	d.post, d.postFile, d.follow = next.post, next.file, anchorPost
	if next.file != "" {
		d.follow = anchorFile
	}
	return m
}

// selectable are those of a list of files the selection can stop at: the
// ones whose id the server sent (C10), each once.
func selectable(files []api.Attachment) []api.Attachment {
	seen := make(map[string]bool, len(files))
	return slices.DeleteFunc(slices.Clone(files), func(f api.Attachment) bool {
		if f.ID == nil || *f.ID == "" || seen[*f.ID] {
			return true
		}
		seen[*f.ID] = true
		return false
	})
}

// openable are the files of the page shown that can be opened. None while
// their page loads.
func (d *detailView) openable() []api.Attachment {
	if d.attachmentsLoading() {
		return nil
	}
	return selectable(d.record.Attachments)
}

// hidden: the server said the comment is missing (commentMissing), and the
// pane keeps nothing of it.
func (d *detailView) hidden(post string) bool {
	e := d.expansion
	return e != nil && e.post == post && e.unavailable
}

// commentFiles are the files of a comment that can be opened.
func (d *detailView) commentFiles(p api.Post) []api.Attachment {
	if d.hidden(p.ID) {
		return nil
	}
	return selectable(p.Attachments)
}

// commentFile is the file the walk stands on, from the record shown: none
// on the comment itself, while the comments load, or when the file is no
// longer there.
func (d *detailView) commentFile() (api.Attachment, bool) {
	p := d.selectedPost()
	if p == nil || d.postFile == "" || d.commentsLoading() {
		return api.Attachment{}, false
	}
	files := d.commentFiles(*p)
	at := slices.IndexFunc(files, func(f api.Attachment) bool { return *f.ID == d.postFile })
	if at < 0 {
		return api.Attachment{}, false
	}
	return files[at], true
}

// selectedFile is the one answer to which file is selected, what the mark
// shows and Enter opens: in the attachments the selected one, kept by its
// id; when it is not on the page shown, the page's first. So a page turn
// and a refresh need no step of their own, and a selection never names a
// file the pane does not show. In the comments the one the walk stands on,
// none on a comment. The kind is set here, by the section, never by data.
func (d *detailView) selectedFile() (launch.File, bool) {
	switch d.area {
	case areaAttachments:
		if files := d.openable(); len(files) > 0 {
			f := files[max(0, d.fileIndex(files))]
			return launch.File{Kind: launch.TicketFile, ID: *f.ID, Name: f.Filename}, true
		}
	case areaComments:
		if f, ok := d.commentFile(); ok {
			return launch.File{Kind: launch.CommentFile, ID: *f.ID, Name: f.Filename}, true
		}
	}
	return launch.File{}, false
}

// fileIndex is the place of the selected file among files, or -1.
func (d *detailView) fileIndex(files []api.Attachment) int {
	return slices.IndexFunc(files, func(f api.Attachment) bool { return *f.ID == d.file })
}

// moveFile moves the selection among the files of the page shown, and
// keeps it in sight.
func (m Model) moveFile(delta int) Model {
	d := m.app.detail
	files := d.openable()
	if len(files) == 0 {
		return m
	}
	at := max(0, d.fileIndex(files))
	d.file = *files[max(0, min(len(files)-1, at+delta))].ID
	d.follow = anchorFile
	return m
}

// --- pages and order ---------------------------------------------------------

// canTurn says whether the active section has a page delta away, by the
// server's count of pages, with nothing of the ticket loading.
func (m Model) canTurn(delta int) bool {
	a := m.app
	d := a.detail
	if d == nil || !d.known || d.loading || !a.pagedOn() || !pagedKey(d.key) {
		return false
	}
	switch d.area {
	case areaComments:
		target := d.key.PostsPage + delta
		return target >= 1 && target <= max(1, d.record.PostsPages)
	case areaAttachments:
		target := d.key.AttachmentsPage + delta
		return target >= 1 && target <= max(1, d.record.AttachmentsPages)
	}
	return false
}

// turnArea is [ or ] in the ticket: the page of the active section.
func (m Model) turnArea(delta int) (Model, tea.Cmd) {
	if !m.canTurn(delta) {
		return m, nil
	}
	d := m.app.detail
	k := d.key
	if d.area == areaComments {
		k.PostsPage += delta
	} else {
		k.AttachmentsPage += delta
	}
	return m, m.app.showPage(k)
}

// canOrder: the comments are the active section, paged, more than one.
func (m Model) canOrder() bool {
	a := m.app
	d := a.detail
	return d != nil && d.known && !d.loading && a.pagedOn() && pagedKey(d.key) && d.area == areaComments && d.record.PostsTotal > 1
}

// switchOrder is o: the other order, from the first page of comments; the
// page of attachments stays.
func (m Model) switchOrder() (Model, tea.Cmd) {
	if !m.canOrder() {
		return m, nil
	}
	k := m.app.detail.key
	k.Newest, k.PostsPage = !k.Newest, 1
	return m, m.app.showPage(k)
}

// showPage asks for another page of the ticket: shown from the cache at
// once when it is there, then read once as the person's. Until it comes,
// the sections it changes say they load; the rest of the ticket stays.
func (a *app) showPage(k cache.Key) tea.Cmd {
	d := a.detail
	a.cancelArea(gateway.Detail)
	d.key, d.corrected, d.err = k, false, present.Inline{}
	if e, ok := a.cachedPage(d, k); ok {
		d.fromCache(e)
	} else {
		a.diskRead(gateway.Detail, k, forDetail)
	}
	d.loading = true
	return a.issue(gateway.Detail, k, forDetail, queue.User)
}

// correctDetailPage reacts to a page beyond the last one the server
// counts, in either section, as the lists do: one read of the last pages,
// both sections in it; a second shrink stops and offers R; a section with
// nothing in it goes to page 1 without a read.
func (m Model) correctDetailPage() (Model, tea.Cmd) {
	a := m.app
	d := a.detail
	k, t := d.key, d.record
	if !pagedKey(k) {
		return m, nil
	}
	fix, ask := k, false
	switch {
	case t.PostsTotal == 0:
		fix.PostsPage = 1
	case k.PostsPage > max(1, t.PostsPages):
		// Never page 0: a server that counts comments but no pages is
		// asked for the first (review round 1, finding 2).
		fix.PostsPage, ask = max(1, t.PostsPages), true
	}
	switch {
	case t.AttachmentsTotal == 0:
		fix.AttachmentsPage = 1
	case k.AttachmentsPage > max(1, t.AttachmentsPages):
		fix.AttachmentsPage, ask = max(1, t.AttachmentsPages), true
	}
	if fix == k {
		return m, nil
	}
	a.forget(k)
	if !ask {
		// Nothing to read: an empty section looks the same on page 1.
		d.key, d.shown = fix, fix
		return m, nil
	}
	if d.corrected {
		d.err = uitext.Inline(uitext.ListShrank)
		return m, nil
	}
	d.corrected = true
	d.key, d.loading = fix, true
	return m, a.issue(gateway.Detail, fix, forDetail, queue.User)
}

// --- expansion ---------------------------------------------------------------

// expandTarget names what e would read whole in the active section: the
// description, or the selected comment; only a text shortened on the page
// (by the server, or by the pane's bound, K1), not one expanded or on its
// way already, with the capability on.
func (m Model) expandTarget() (post string, ok bool) {
	a := m.app
	d := a.detail
	if d == nil || !d.known || !a.fullTextOn() {
		return "", false
	}
	// held: the target's expansion loads, is shown, or was found missing;
	// one that failed is offered again.
	e := d.expansion
	held := func(post string) bool { return e != nil && e.post == post && (e.loading || e.known || e.unavailable) }
	switch d.area {
	case areaDescription:
		if _, short := shown(d.record.DescriptionText, maxPageText, d.uncut); short != notShortened && !held("") {
			return "", true
		}
	case areaComments:
		if d.commentsLoading() {
			return "", false
		}
		if p := d.selectedPost(); p != nil {
			if _, short := shown(p.BodyText, maxPageText, d.uncut); short != notShortened && !held(p.ID) {
				return p.ID, true
			}
		}
	}
	return "", false
}

// expand is e: one read of the text whole, under a key of its own; the
// cached answer, if any, is shown at once. Another expansion closes first.
func (m Model) expand() (Model, tea.Cmd) {
	post, ok := m.expandTarget()
	if !ok {
		return m, nil
	}
	a := m.app
	d := a.detail
	a.closeExpansion()
	if d.follow == anchorFile {
		// On a comment's file: the text opens above it, so the comment is
		// what the pane has to show.
		d.follow = anchorPost
	}
	k := d.key
	if post == "" {
		// The description with the options of the page asked (the one
		// shown, or the one on its way): the answer is as large as a page,
		// and valid on its own.
		k.FullText = true
	} else {
		k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage, k.Post = 0, 0, false, 0, post
	}
	e := &expansion{key: k, post: post}
	d.expansion = e
	if entry, ok := a.cachedExpansion(e); ok {
		e.fromCache(entry)
	} else {
		a.diskRead(gateway.Expansion, k, forExpansion)
	}
	e.loading = true
	return m, a.issue(gateway.Expansion, k, forExpansion, queue.User)
}

// cachedExpansion puts the memory entry of the expansion's key into it, if
// there is one that still reads: the ticket for the description, the
// comment alone for one comment (the form both ways file, K1). An entry
// that does not read, one comment kept as a ticket before K1 among them,
// goes.
func (a *app) cachedExpansion(e *expansion) (cache.Entry, bool) {
	entry, ok := a.store.Get(e.key)
	if !ok {
		return cache.Entry{}, false
	}
	kept := transport.Answer{Class: transport.Object, Payload: entry.Payload, Status: 200, Sent: true}
	if e.post != "" {
		post, ans := api.DecodePost(kept)
		if !ans.OK() || gateway.CheckPost(e.key, post) != nil {
			a.forget(e.key)
			return cache.Entry{}, false
		}
		e.text, e.uncut = post.BodyText, false
		return entry, true
	}
	record, ans := api.DecodeTicket(kept, gateway.DetailOptions(e.key))
	if !ans.OK() || gateway.CheckDetail(e.key, record) != nil {
		a.forget(e.key)
		return cache.Entry{}, false
	}
	e.text, e.uncut = record.DescriptionText, false
	return entry, true
}

// expandedText is the text a result brings: the description, or the one
// comment (the gateway made sure it is the one asked for).
func expandedText(r gateway.TicketResult, post string) string {
	if post == "" {
		return r.Record.DescriptionText
	}
	return r.Post.BodyText
}

// receiveExpansion applies the answer of an expansion: its text, a
// failure on the expansion, the missing-comment rule, or the ticket gone.
func (m Model) receiveExpansion(r gateway.TicketResult) (Model, tea.Cmd) {
	a := m.app
	dec, ok := m.receive(r)
	if !ok {
		return m, nil
	}
	if dec == cache.Replace && r.Key.Post == "" {
		// The description whole comes with the ticket: an accepted one is
		// a read of its title too, whatever became of the pane meanwhile.
		// One comment is a read of the comment alone (K1).
		a.learnTitle(r.Key.Ticket, r.Record.Title, uint64(r.Flight))
	}
	d := a.detail
	if d == nil {
		return m, nil
	}
	if dec == cache.Remove {
		// No access or no ticket, also for one comment: every variant of
		// the ticket left the cache (Scope.Receive), the pane says so.
		if d.key.Resource() == r.Key.Resource() {
			m = m.detailRemoved()
		}
		return m, nil
	}
	e := d.expansion
	if e == nil || e.key != r.Key {
		return m, nil
	}
	switch {
	case dec == cache.Replace:
		e.text, e.unavailable, e.uncut = expandedText(r, e.post), false, r.Way == features.REST
		e.fresh(a.now())
	case e.post != "" && r.Answer.Class == transport.Refused && r.Answer.Reason == api.CommentNotFound:
		return m.commentMissing(r)
	default:
		e.failed(dec, r.Answer)
	}
	return m, nil
}

// commentMissing is the missing-comment rule: the cached expansion goes
// and its text is hidden at once, no older answer for it is taken any
// more (the area moves on), the ticket stays and says so, and it is read
// once without post_id, sent after the answer; only that read may remove
// it. The answer is not
// filed as the newest of the ticket: a plain read sent before it must
// still be able to remove the ticket (review round 1, finding 1).
func (m Model) commentMissing(r gateway.TicketResult) (Model, tea.Cmd) {
	a := m.app
	d := a.detail
	a.forget(r.Key)
	a.cancelArea(gateway.Expansion)
	e := d.expansion
	e.text, e.status, e.unavailable = "", status{}, true
	// The read that may remove the ticket is sent now: a read of the page
	// that left before (its answer may predate the deletion, or it waits
	// for the previews' budget) is canceled, and the new
	// epoch keeps the queue from joining it (review round 2, finding 1).
	a.cancelArea(gateway.Detail)
	a.scope.Invalidate(d.key)
	d.loading = true
	return m, a.issue(gateway.Detail, d.key, forDetail, queue.User)
}

// detailRemoved shows that the ticket is gone: its record and expansion
// go, an open closes, the focus returns to the list.
func (m Model) detailRemoved() Model {
	a := m.app
	d := a.detail
	a.closeExpansion()
	d.record, d.shown, d.flight = api.Ticket{}, cache.Key{}, 0
	d.removed()
	if t := a.tickets; t != nil && t.opened == d.id {
		t.opened = ""
	}
	if m.focus == view.Object {
		m.focus = view.List
	}
	return m
}
