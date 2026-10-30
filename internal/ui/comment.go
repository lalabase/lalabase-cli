package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// A comment on a ticket (K3a): k on the known ticket in the focused pane opens
// the large dialog, Ctrl-S posts its text, plain, over REST where /me names
// posts.plain, else through the tool with format; the server makes the markup.
// A server with neither offers no k.
//
// Every press of Ctrl-S is one operation with its own idempotency key, under
// the lock and with the ends every write on a ticket has (write.go). An unknown
// end holds the box and the lock until the reads that clear it up have landed,
// and is never sent again: the text stays a draft, and k with it is a new action
// of a person who sees the ticket as read again.
//
// The draft is what esc leaves, in memory alone, per ticket; a confirmed comment
// takes it.

// commentLimit is the field's limit, in runes (C2's decision).
const commentLimit = 10000

// commentAllowed: the token may write and the server takes plain text, on
// either way.
func (a *app) commentAllowed() bool {
	return a.tokenWrites() && a.on("comment_plain") && a.deps.Gateway != nil
}

// commentWrites is the comment among the writes on a ticket (write.go).
func (a *app) commentWrites() ticketWrites {
	return ticketWrites{
		kind: gateway.Comment, name: "comment", ops: a.comments, busy: uitext.CommentBusy,
		clearing: func(op *ticketOp) bool { return a.clarifying(op.clarifiers) },
		done: func(op *ticketOp, r gateway.WriteResult) (present.Inline, tea.Cmd) {
			delete(a.drafts, op.ticket)
			return uitext.Fill(uitext.CommentDone, op.number), a.afterComment(op, r.Comment.ID)
		},
		unclear: func(op *ticketOp) tea.Cmd { return a.afterComment(op, "") },
		held: func(op *ticketOp) (present.Inline, present.Style) {
			if a.clarifying(op.clarifiers) {
				return uitext.Inline(uitext.CommentUnclearReading), present.NoticeWaiting
			}
			return uitext.Inline(uitext.CommentUnclear), present.NoticeProblem
		},
		why:     notPosted,
		notDone: uitext.CommentNotPosted, unclearNotice: uitext.CommentUnclearNotice,
	}
}

// notPosted is why a comment did not happen. The tool's refusal has its text
// in the answer's message, where REST has the body.
func notPosted(end writeEnd, r gateway.WriteResult) present.Inline {
	if end == writeRefused && r.Way == features.MCP && r.Answer.Message != "" {
		return present.NewInline(bounded(r.Answer.Message, maxRefusal))
	}
	return notDone(notDoneWords{uitext.CommentNotSent, uitext.CommentRateLimited, uitext.CommentForbidden}, end, r.Answer)
}

// commentTarget is the ticket k would comment on right now.
func (m Model) commentTarget() (DialogTarget, bool) {
	a := m.app
	if a == nil || !a.commentAllowed() {
		return DialogTarget{}, false
	}
	return m.freeTarget(a.commentWrites())
}

// pressK is k: the dialog on the ticket in the pane, which it holds from here
// on, with the ticket's draft in it.
func (m Model) pressK() Model {
	t, ok := m.commentTarget()
	if !ok {
		return m
	}
	a := m.app
	return m.openTicketDialog(DialogSpec{
		Action: uitext.Inline(uitext.CommentAction),
		Large:  true,
		Fields: []DialogField{{Label: uitext.Inline(uitext.CommentText), Hint: uitext.Inline(uitext.CommentHint), Limit: commentLimit, Value: a.drafts[t.Ticket], Required: true, Tidy: withoutBlankEdges}},
		Send:   a.sendComment,
		Left:   a.keepDraft,
	})
}

// sendComment is one press of Ctrl-S.
func (a *app) sendComment(s Submission) tea.Cmd {
	return a.sendOnTicket(a.commentWrites(), s, gateway.WriteTicket{Value: s.Values[0], Way: gateway.RoutesOf(*a.report).Comment})
}

// keepDraft is esc: the text as it stands stays with the ticket.
func (a *app) keepDraft(s Submission) {
	if s.Values[0] == "" {
		delete(a.drafts, s.Target.Ticket)
		return
	}
	a.drafts[s.Target.Ticket] = s.Values[0]
}

// withoutBlankEdges takes the blank lines off both ends of a text and leaves
// the rest as it is; a text of blanks alone is empty.
func withoutBlankEdges(text string) string {
	lines := strings.Split(text, "\n")
	blank := func(line string) bool { return strings.TrimSpace(line) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// receiveComment files what became of a comment.
func (m Model) receiveComment(r gateway.WriteResult) (Model, tea.Cmd) {
	if m.app == nil {
		return m, nil
	}
	return m.endOnTicket(m.app.commentWrites(), r)
}

// afterComment follows a confirmed comment and an unknown one alike. The
// ticket and the project's list leave the cache, so do the inboxes the server
// changed: it marks the author's rows of the ticket read (core #989). What is
// shown of them is read again, the person's priority, and those reads are the
// operation's clarifiers. A ticket still in the pane shows its first page of
// comments, newest first, with the comment selected (the server's newest
// when nobody knows the id). Elsewhere the next open reads it.
func (a *app) afterComment(op *ticketOp, comment string) tea.Cmd {
	a.dropResources(a.detailKey(op.project, op.ticket).Resource(), a.ticketsKey(op.project, "", 1).Resource())
	read, issued := a.issuedBy(func() tea.Cmd {
		cmds := a.rereadInbox(op.org)
		if v := a.tickets; v != nil && v.project == op.project {
			cmds = append(cmds, a.reloadTickets())
		}
		if d := a.detail; d != nil && d.id == op.ticket && d.project == op.project {
			d.area, d.post, d.follow = areaComments, comment, anchorPost
			k := d.key
			if pagedKey(k) {
				k.PostsPage, k.Newest = 1, true
			}
			cmds = append(cmds, a.showPage(k))
		}
		return tea.Batch(cmds...)
	})
	op.clarifiers = issued
	return read
}
