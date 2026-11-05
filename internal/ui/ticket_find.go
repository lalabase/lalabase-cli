package ui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The find in the ticket (F2), with the meeting's pieces (find.go): the
// description as shown, the comments of every page in the pane's order,
// the files of the attachments page shown. The page shown is searched as
// the pane draws it, an expansion included. The other pages of comments
// are read on Enter with the pane's own key, so each is the entry [ and ]
// would show: one after the other, at the person's priority, memory and
// disk first, at most maxFindPages; each is searched once per record and
// query, in the lines ticketText builds for it. A hit is known by where it
// stands (findPlace), so a page read later, a page that moved or an
// expansion keeps the current one on its text.

// maxFindPages bounds the pages of comments a find reads: 40 of 25.
const maxFindPages = 40

// findPlace is where a hit stands: in the description, in a comment (by
// id) or in the files, the k-th there; area ticketAreas for the head or a
// heading, which the find does not count.
type findPlace struct {
	area ticketArea
	post string
	k    int
}

// foundState is how far the find is with a page of comments.
type foundState uint8

const (
	foundWaiting foundState = iota
	foundRead
	foundFailed
)

// foundPage is a page of comments of the find: its comments in order, each
// with its number of hits.
type foundPage struct {
	state foundState
	posts []postHits
}

type postHits struct {
	id   string
	hits int
}

// findRun is a run of hits in one text, in the find's order: the
// description, a comment (page 0 on the page drawn), the files.
type findRun struct {
	area ticketArea
	post string
	page int
	hits int
}

// shownHit is a hit on the page drawn and its first screen line.
type shownHit struct {
	place findPlace
	line  int
}

// ticketFind is the find beyond its line: the key its pages are read with,
// the pages (none without paging or with one page), the page out (0 for
// none), the current hit once one is set; runs and shown are of the pane's
// last build.
type ticketFind struct {
	key     cache.Key
	pages   []foundPage
	reading int
	at      findPlace
	set     bool
	runs    []findRun
	shown   []shownHit
}

func (f *ticketFind) total() int {
	n := 0
	for _, r := range f.runs {
		n += r.hits
	}
	return n
}

// --- the pane ----------------------------------------------------------------

// placesIn says where each hit of a ticket's lines stands, and counts the
// hits of each text.
func placesIn(t ticketLines, d *detailView, hits []hit) ([]findPlace, map[findPlace]int) {
	var starts []int
	var ids []string
	if !d.commentsLoading() {
		for _, p := range d.record.Posts {
			if at, ok := t.posts[p.ID]; ok {
				starts, ids = append(starts, at), append(ids, p.ID)
			}
		}
	}
	places, count := make([]findPlace, len(hits)), map[findPlace]int{}
	for i, h := range hits {
		p := findPlace{area: ticketAreas}
		switch {
		case h.line > t.areas[areaDescription] && h.line < t.areas[areaComments]:
			p.area = areaDescription
		case h.line > t.areas[areaAttachments]:
			p.area = areaAttachments
		case h.line > t.areas[areaComments] && h.line < t.areas[areaAttachments]:
			// The comment whose lines hold it: the last one starting at or
			// above it.
			if j, found := slices.BinarySearch(starts, h.line); found || j > 0 {
				if !found {
					j--
				}
				p.area, p.post = areaComments, ids[j]
			}
		}
		if p.area != ticketAreas {
			text := findPlace{area: p.area, post: p.post}
			p.k, count[text] = count[text], count[text]+1
		}
		places[i] = p
	}
	return places, count
}

// findRuns are the find's hits in order: the description, the comments
// page by page (the page drawn as drawn; a comment on two pages, one of
// them older, once), the files.
func (d *detailView) findRuns(count map[findPlace]int) []findRun {
	f := &d.found
	runs := []findRun{{area: areaDescription, hits: count[findPlace{area: areaDescription}]}}
	live := 0
	if !d.commentsLoading() && (f.pages == nil || d.shown.Newest == f.key.Newest) {
		live = max(1, d.shown.PostsPage)
	}
	seen := map[string]bool{}
	add := func(id string, page, hits int) {
		if !seen[id] {
			seen[id] = true
			runs = append(runs, findRun{area: areaComments, post: id, page: page, hits: hits})
		}
	}
	for page := 1; page <= max(len(f.pages), live); page++ {
		switch {
		case page == live:
			for _, p := range d.record.Posts {
				add(p.ID, 0, count[findPlace{area: areaComments, post: p.ID}])
			}
		case page <= len(f.pages):
			for _, p := range f.pages[page-1].posts {
				add(p.id, page, p.hits)
			}
		}
	}
	return append(runs, findRun{area: areaAttachments, hits: count[findPlace{area: areaAttachments}]})
}

// indexOf is the place's index among the hits of runs, -1 when its text
// has none any more; a text with fewer hits keeps it on its last.
func indexOf(runs []findRun, at findPlace) int {
	n := 0
	for _, r := range runs {
		if r.area == at.area && r.post == at.post && r.hits > 0 {
			return n + min(at.k, r.hits-1)
		}
		n += r.hits
	}
	return -1
}

// placeAt is the place of the i-th hit among runs, and its page (0 for
// the page drawn).
func placeAt(runs []findRun, i int) (findPlace, int) {
	for _, r := range runs {
		if i < r.hits {
			return findPlace{area: r.area, post: r.post, k: i}, r.page
		}
		i -= r.hits
	}
	return findPlace{area: ticketAreas}, 0
}

// countFind finds the query in the pane's lines t: the hits, where each
// stands, the runs over every page, and the index of the current hit, -1
// for none.
func (d *detailView) countFind(t ticketLines) ([]hit, []findPlace, int) {
	f := &d.found
	hits := findHits(t.lines, d.find.query)
	places, count := placesIn(t, d, hits)
	f.runs = d.findRuns(count)
	at := -1
	if f.set {
		if at = indexOf(f.runs, f.at); at < 0 {
			// Its comment left the pages held: the index stays, within the
			// hits there are now.
			at = d.find.at(f.total())
		}
		d.find.current = max(0, at)
	}
	return hits, places, at
}

// findInPane marks the hits in the pane's text, the current one apart, and
// wraps it once so they are found on the screen: the current one is kept
// in sight while it is the anchor.
func (m Model) findInPane(c *present.Column, d *detailView, t ticketLines, g view.Geometry) {
	f := &d.found
	hits, places, at := d.countFind(t)
	now, _ := placeAt(f.runs, at)
	var drawn []hit
	var drawnAt []findPlace
	cur := -1
	for i, h := range hits {
		if places[i].area == ticketAreas {
			continue
		}
		if at >= 0 && places[i] == now {
			cur = len(drawn)
		}
		drawn, drawnAt = append(drawn, h), append(drawnAt, places[i])
	}
	c.Styles = markHits(t.styles, drawn, present.Match)
	if cur >= 0 {
		c.Styles = markHits(c.Styles, drawn[cur:cur+1], present.MatchCurrent)
	}
	var origins []view.Origin
	c.Wrapped, c.Styles, origins = view.Wrap(*c, view.TextWidth(*c, g, view.Object), m.widthMethod)
	placed := placeHits(drawn, c.Wrapped, origins)
	f.shown = f.shown[:0]
	for i, parts := range placed {
		if len(parts) > 0 {
			f.shown = append(f.shown, shownHit{place: drawnAt[i], line: parts[0].line})
		}
	}
	if d.follow == anchorMatch && cur >= 0 && len(placed[cur]) > 0 {
		c.Offset = placed[cur][0].line
	}
}

// ticketNotice is the pane's notice with a find: its line while it is
// open, else the notice, then where the find stands and how far it read.
func (m Model) ticketNotice(d *detailView, notice present.Inline) present.Inline {
	if m.mode == FindEntry {
		return d.find.line()
	}
	if d.find.query == "" {
		return notice
	}
	f := &d.found
	place := d.find.place(f.total())
	if !f.set && f.total() > 0 {
		// Nothing on the page shown: n goes to the first.
		place = uitext.Fill(uitext.FindFound, f.total())
	}
	words := []string{place.String()}
	var failed []int
	for i, p := range f.pages {
		if p.state == foundFailed {
			failed = append(failed, i+1)
		}
	}
	switch {
	case f.reading > 0:
		words = append(words, uitext.Fill(uitext.FindReading, f.reading, len(f.pages)).String())
	case len(f.pages) > 0 && d.record.PostsPages > len(f.pages):
		comments := 0
		for _, r := range f.runs {
			comments += boolInt(r.area == areaComments)
		}
		words = append(words, uitext.Fill(uitext.FindSearched, comments, d.record.PostsTotal).String())
	}
	switch {
	case len(failed) == 1:
		words = append(words, uitext.Fill(uitext.FindPageFailed, failed[0]).String())
	case len(failed) > 1:
		words = append(words, uitext.Fill(uitext.FindPagesFailed, len(failed)).String())
	}
	text := strings.Join(words, ", ")
	if notice.String() != "" {
		text = notice.String() + " · " + text
	}
	return present.NewInline(text)
}

// ticketHits is the number of hits of the find in the ticket, counted as
// the pane counts them but not drawn: the help asks on every frame. None
// without a query.
func (m Model) ticketHits() int {
	a := m.app
	d := a.detail
	if d == nil || !d.known || d.find.query == "" {
		return 0
	}
	// The head is not searched: the facts, the agent and the notification
	// it would hold change no hit's place.
	d.countFind(ticketText(d, ticketContext{paged: a.pagedOn(), when: a.when, me: a.ownID()}))
	return d.found.total()
}

// --- the keys ----------------------------------------------------------------

// findInTicket is Enter on the find line, and o with a find: the find
// starts again from the top line of the pane. The page shown is known at
// once: its first hit at or below top is the current one, else its first,
// round the end; the other pages are read.
func (m Model) findInTicket(top int) tea.Cmd {
	a := m.app
	d := a.detail
	a.cancelArea(gateway.FindPages)
	d.found, d.offset = ticketFind{}, top
	if d.follow == anchorMatch {
		d.follow = anchorNone
	}
	if d.find.query == "" {
		return nil
	}
	if n := min(d.record.PostsPages, maxFindPages); a.pagedOn() && pagedKey(d.key) && n > 1 {
		d.found.key, d.found.pages = d.key, make([]foundPage, n)
		if !d.commentsLoading() {
			a.takePage(d, d.shown, d.record, d.uncut)
		}
	}
	_ = m.detailColumn()
	if f := &d.found; len(f.shown) > 0 {
		f.at, f.set, d.find.round = f.shown[0].place, true, 1
		for _, h := range f.shown {
			if h.line >= top {
				f.at, d.find.round = h.place, 0
				break
			}
		}
		d.follow = anchorMatch
	}
	return a.readFindPages()
}

// moveTicketHit is n or N: the next or the previous hit round the end, in
// sight; one on another page turns the pane there. With none current yet
// n takes the first, N the last.
func (m Model) moveTicketHit(delta int) (Model, tea.Cmd) {
	a := m.app
	d := a.detail
	n := m.ticketHits()
	if n == 0 {
		return m, nil
	}
	f := &d.found
	switch {
	case f.set:
		d.find.move(delta, n)
	case delta > 0:
		d.find.current, d.find.round = 0, 0
	default:
		d.find.current, d.find.round = n-1, 0
	}
	at, page := placeAt(f.runs, d.find.current)
	f.at, f.set, d.follow = at, true, anchorMatch
	if page == 0 {
		return m, nil
	}
	k := d.key
	k.PostsPage = page
	return m, a.turnToHit(k)
}

// turnToHit turns the pane to page k of the comments for n or N: as memory
// holds it when a read of this run filed it, so nothing is read again;
// else as [ and ] turn it.
func (a *app) turnToHit(k cache.Key) tea.Cmd {
	d := a.detail
	if a.filed[k] != 0 {
		if e, ok := a.cachedPage(d, k); ok {
			a.cancelArea(gateway.Detail)
			d.key, d.corrected, d.err = k, false, present.Inline{}
			d.fromCache(e)
			d.cached, d.loading = false, false
			return nil
		}
	}
	return a.showPage(k)
}

// endTicketFind is Esc with a find on the ticket: the marks go, the pane
// stays where it is.
func (m Model) endTicketFind() Model {
	d := m.app.detail
	d.offset = m.paneTop()
	if d.follow == anchorMatch {
		d.follow = anchorNone
	}
	m.app.endFindPages(d)
	return m
}

// endFindPages ends the find on d, its reads out canceled.
func (a *app) endFindPages(d *detailView) {
	a.cancelArea(gateway.FindPages)
	d.find, d.found = finder{}, ticketFind{}
}

// --- the pages ---------------------------------------------------------------

// readFindPages goes on with the find's pages in order: each from memory
// at once, the next one not there from the disk, else the server; one at
// a time.
func (a *app) readFindPages() tea.Cmd {
	d := a.detail
	if d == nil {
		return nil
	}
	f := &d.found
	for f.reading == 0 {
		at := slices.IndexFunc(f.pages, func(p foundPage) bool { return p.state == foundWaiting })
		if at < 0 {
			return nil
		}
		k := f.key
		k.PostsPage = at + 1
		if a.findFromMemory(d, k) {
			continue
		}
		f.reading = at + 1
		if a.diskRead(gateway.FindPages, k, forFindPage) {
			return nil
		}
		return a.issue(gateway.FindPages, k, forFindPage, queue.User)
	}
	return nil
}

// findFromMemory takes page k from memory, if it holds one that reads.
func (a *app) findFromMemory(d *detailView, k cache.Key) bool {
	record, _, ok := a.cachedRecord(k)
	if ok {
		a.takePage(d, k, record, false)
	}
	return ok
}

// takePage takes a page of comments of the find's order: its hits found in
// the lines ticketText builds for it, nothing selected or expanded.
func (a *app) takePage(d *detailView, k cache.Key, record api.Ticket, uncut bool) {
	f := &d.found
	at := k.PostsPage - 1
	if d.find.query == "" || k.Newest != f.key.Newest || at < 0 || at >= len(f.pages) {
		return
	}
	page := &detailView{key: k, shown: k, record: record, uncut: uncut}
	t := ticketText(page, ticketContext{paged: true, when: a.when, me: a.ownID()})
	_, count := placesIn(t, page, findHits(t.lines, d.find.query))
	posts := make([]postHits, len(record.Posts))
	for i, p := range record.Posts {
		posts[i] = postHits{id: p.ID, hits: count[findPlace{area: areaComments, post: p.ID}]}
	}
	f.pages[at] = foundPage{state: foundRead, posts: posts}
}

// out reports whether k is the find's page out.
func (f *ticketFind) out(k cache.Key) bool {
	want := f.key
	want.PostsPage = f.reading
	return f.reading > 0 && k == want
}

// settleFindPages follows the find's page out once nothing reads it any
// more: the disk answered (a hit is in memory now), failed, or dropped the
// read. The page comes from memory if it is there, else from the server;
// no page waits for an answer that will not come. Every step ends with it.
func (a *app) settleFindPages() tea.Cmd {
	d := a.detail
	if d == nil || d.found.reading == 0 {
		return nil
	}
	k := d.found.key
	k.PostsPage = d.found.reading
	for _, w := range a.waiters {
		if !w.canceled && w.purpose == forFindPage && w.ticket.Key == k {
			return nil
		}
	}
	if disk := a.disk; disk != nil {
		for _, r := range disk.reads {
			if !r.canceled && r.purpose == forFindPage && r.ticket.Key == k {
				return nil
			}
		}
	}
	if a.findFromMemory(d, k) {
		d.found.reading = 0
		return a.readFindPages()
	}
	return a.issue(gateway.FindPages, k, forFindPage, queue.User)
}

// receiveFindPage takes a page the find read, or that it did not load;
// then the next. A removal is the ticket's, as for the pane's own reads.
func (m Model) receiveFindPage(r gateway.TicketResult) (Model, tea.Cmd) {
	a := m.app
	dec, ok := m.receive(r)
	if !ok {
		return m, nil
	}
	if dec == cache.Replace {
		a.learnTitle(r.Record.ID, r.Record.Title, uint64(r.Flight))
	}
	d := a.detail
	if d == nil || d.key.Resource() != r.Key.Resource() {
		return m, nil
	}
	out := d.found.out(r.Key)
	if out {
		d.found.reading = 0
	}
	switch {
	case dec == cache.Remove:
		return m.detailRemoved(), nil
	case !out:
		return m, nil
	case dec == cache.Replace:
		a.takePage(d, r.Key, r.Record, r.Way == features.REST)
	default:
		d.found.pages[r.Key.PostsPage-1].state = foundFailed
	}
	return m, a.readFindPages()
}

// retryFindPages is R with pages of the find that did not load: they are
// read again, after the one out.
func (a *app) retryFindPages(d *detailView) tea.Cmd {
	for i := range d.found.pages {
		if d.found.pages[i].state == foundFailed {
			d.found.pages[i].state = foundWaiting
		}
	}
	return a.readFindPages()
}
