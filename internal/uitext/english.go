// Package uitext holds every text of the interface, in English, in one
// place. A text reaches the screen only as a present value, so a mislabelled
// foreign string in the catalogue could still not bypass the cleaning.
package uitext

import (
	"fmt"

	"github.com/gboyke/lalabase-cli/internal/present"
)

// ID names a text. uint16: the catalogue passed 255 texts with C9c.
type ID uint16

const (
	App ID = iota
	DefaultProfile
	NotLoaded
	NoData
	NoItem
	Navigation
	List
	Object
	Chooser
	Organisation
	TooSmall
	Ellipsis
	QuitKey
	QuitHelp
	InterruptKey
	InterruptHelp
	FocusKey
	FocusHelp
	EnterKey
	EnterHelp
	EscapeKey
	EscapeHelp
	TTYError
	ProfileFlagError
	LocaleError
	ProfileError
	RunError
	// C1b-2: organisations, counters and the unread inbox.
	Organisations
	Inbox
	Details
	AllOrganisations
	UnknownCount
	CountValue
	VerifyingIdentity
	CanWriteYes
	CanWriteNo
	CanWriteUnknown
	UnknownUser
	UnnamedOrganisation
	IdentityFailed
	CapabilitiesFailed
	OrganisationsFailed
	CountsFailed
	UnexpectedResponse
	RefusedWith
	ServerUnreachable
	SessionExpired
	BelowTested
	InboxUnsupported
	FilterUnavailable
	RateLimited
	LoadingOrganisations
	ReadingCapabilities
	Loading
	Refreshing
	Cached
	CachedStale
	PossiblyStale
	NoUnread
	NoneOnPage
	NoOrganisations
	PageOf
	NotFoundRemoved
	InboxShrank
	NoMessage
	UnknownActor
	NoProject
	UnnamedProject
	NoTicket
	UntitledTicket
	TimeUnknown
	TicketNumber
	LabelMessage
	LabelActor
	LabelOrganisation
	LabelProject
	LabelTicket
	LabelEvent
	LabelNotification
	LabelState
	LabelLastEvent
	StateUnread
	StateRead
	UpDownKey
	HighlightHelp
	SelectHelp
	OpenHelp
	DetailsHelp
	PageKey
	PageHelp
	PrevPageKey
	PrevPageHelp
	NextPageKey
	NextPageHelp
	RefreshKey
	RefreshHelp
	RetryHelp
	PageUpDownKey
	ScrollHelp
	MoveHelp
	CloseHelp
	OrganisationsHelp
	InboxHelp
	// C1c: the tickets section.
	Tickets
	Projects
	Ticket
	TicketsUnsupported
	TicketUnsupported
	NoProjects
	NoTickets
	NoMatches
	NoneOnTicketPage
	Archived
	NoStatus
	Unassigned
	NoDescription
	NoComments
	NoAttachments
	NoText
	Private
	UnnamedFile
	CommentsPaged
	CommentsFirst
	CommentsAll
	AttachmentsPaged
	AttachmentsFirst
	AttachmentsAll
	MetaAssigned
	MetaUnassigned
	MetaCreator
	MetaUpdated
	SearchFor
	SearchLine
	Caret
	ListShrank
	OpenTicketsHelp
	OpenTicketHelp
	TicketsHelp
	ProjectsHelp
	ListHelp
	BackHelp
	SearchKey
	SearchHelp
	CancelHelp
	BackspaceKey
	DeleteHelp
	// C1d: the ticket's sections, pages, order and expansion.
	Description
	WholeText
	NewestFirst
	OldestFirst
	TextShortened
	// K1: a text longer than the pane shows (REST sends every text whole).
	TextCut
	CommentUnavailable
	LoadingWholeText
	LoadingPage
	PageNotLoaded
	ActiveArea
	SelectedComment
	TypeUnknown
	SizeUnknown
	SizeBytes
	SizeKiB
	SizeMiB
	SizeGiB
	TextAvailable
	TextUnsupported
	TextUnavailable
	TextEmpty
	TextStatusUnknown
	TabKey
	NextAreaHelp
	OrderKey
	ExpandKey
	ExpandHelp
	CollapseHelp
	CommentHelp
	// C1f: mark as read, and the ticket opened from a notification.
	MarkReadKey
	MarkReadHelp
	OpenLinkedTicketHelp
	NotificationHelp
	LabelMarkRead
	Marking
	Marked
	MarkRefused
	MarkNotSent
	MarkRateLimited
	MarkUnknownRefreshing
	MarkUnknown
	WritePermissionUnknown
	ReadOnlyToken
	MarkUnsupported
	MarkingShort
	MarkedShort
	NotMarkedShort
	UnknownShort
	NotificationUnread
	NotificationRead
	// C1e: the disk cache.
	DiskUnavailable
	DiskBusy
	SessionExpiredDiskKept
	// C7: Claude Code beside the ticket.
	LaunchKey
	LaunchHelp
	Launching
	LaunchOpened
	LaunchEnded
	LaunchEndedError
	LaunchNotHandedOver
	LaunchNoFolder
	LaunchNotADirectory
	LaunchBadFolder
	LaunchNoFiles
	LaunchNoClaude
	LaunchNoPermission
	LaunchSessionGone
	LaunchNoAnswer
	LaunchFailed
	// Prototype: tmux could not start the agent's session.
	LaunchTmuxFailed
	// C9a: a launch whose agent's state cannot be shown.
	LaunchUntracked
	// C9a: the agent's line in the ticket's head, its states' words and
	// the age of a state.
	AgentLine
	AgentApproval
	AgentWaiting
	AgentLost
	AgentWorking
	AgentStarting
	AgentEnded
	AgeSeconds
	AgeMinutes
	AgeHours
	AgeDays
	// L2: the layout after the design review.
	PathStep
	PathDetail
	You
	BarStatus
	BarProject
	BarPeople
	BarUpdated
	// C9b: the agents across the organisations.
	Agents
	AgentsKey
	AgentsHelp
	BarAgent
	AgentMeta
	// C9c: d removes an ended or lost agent; what became of it, and of a
	// seen mark that could not be written.
	RemoveAgentKey
	RemoveAgentHelp
	AgentRemoved
	AgentNotRemoved
	AgentNotSeen
	AgentsTooMany
	// S6: the meta bar's label of the person's time on a ticket, and the
	// same in the head's line of facts.
	BarBooked
	MetaBooked
	// C9c: x jumps to an agent's pane or starts it again; the hints of
	// the ticket's head and what came of either.
	JumpHelp
	AgainHelp
	AgentHintJump
	AgentHintAgain
	AgentHintRemove
	Jumping
	Jumped
	JumpPaneGone
	JumpNoPane
	JumpFailed
	StartingAgain
	LaunchResumed
	AgentLostPane
	AgainNotStarted
	AgentNotSettled
	// Unlisted is the help of a key that acts but is not named in the
	// help line (internal/ui drops it there).
	Unlisted
	count
)

func Inline(id ID) present.Inline { return present.NewInline(english[id]) }
func Body(id ID) present.Body     { return present.NewBody(english[id]) }

// Fill fills a template's placeholders (%s, %d) and cleans the result like
// every other text: an argument is foreign as often as not.
func Fill(id ID, args ...any) present.Inline {
	return present.NewInline(fmt.Sprintf(english[id], args...))
}

var english = map[ID]string{
	App: "lalabase", DefaultProfile: "default", NotLoaded: "Not loaded",
	NoData: "No data loaded", NoItem: "No item opened", Navigation: "Navigation",
	List: "List", Object: "Object", Chooser: "Choose organisation and section",
	Organisation: "Organisation", TooSmall: "Terminal too small. Resize to at least 24x8.", Ellipsis: "…",
	QuitKey: "q", QuitHelp: "quit", InterruptKey: "ctrl+c", InterruptHelp: "interrupt",
	FocusKey: "left/right", FocusHelp: "focus", EnterKey: "enter", EnterHelp: "list",
	EscapeKey: "esc", EscapeHelp: "back",
	TTYError:         "lalabase: interactive mode requires terminals on stdin and stdout. Run 'lalabase help'.",
	ProfileFlagError: "lalabase: interactive mode supports only --profile default.",
	LocaleError:      "lalabase: interactive mode requires a UTF-8 locale. Check LC_ALL, LC_CTYPE and LANG.",
	ProfileError:     "lalabase: cannot load the default profile. Run 'lalabase auth import', or check the configuration and its permissions.",
	RunError:         "lalabase: terminal interface failed.",

	Organisations: "Organisations", Inbox: "Inbox", Details: "Details",
	AllOrganisations: "All organisations", UnknownCount: "?", CountValue: "%d",
	VerifyingIdentity: "Verifying identity…",
	CanWriteYes:       "Token can write: yes", CanWriteNo: "Token can write: no", CanWriteUnknown: "Token can write: unknown",
	UnknownUser: "Unknown user", UnnamedOrganisation: "Unnamed organisation",
	IdentityFailed:       "Cannot verify identity. R retry.",
	CapabilitiesFailed:   "Cannot read server capabilities. R retry.",
	OrganisationsFailed:  "Cannot load organisations. R retry.",
	CountsFailed:         "Cannot load unread counts. R retry.",
	UnexpectedResponse:   "Unexpected server response: %s",
	RefusedWith:          "Refused: %s",
	ServerUnreachable:    "Could not reach the server. R retry.",
	SessionExpired:       "Session expired. Restart lalabase.",
	BelowTested:          "Server %s is older than the tested %s; its schemas decide what works.",
	InboxUnsupported:     "Inbox is not supported by this server.",
	FilterUnavailable:    "Showing the first page only; organisation filtering is unavailable.",
	RateLimited:          "Rate limited until %s",
	LoadingOrganisations: "Loading organisations…", ReadingCapabilities: "Reading server capabilities…",
	Loading: "Loading…", Refreshing: "Refreshing…",
	Cached: "Cached", CachedStale: "Cached, possibly stale", PossiblyStale: "Possibly stale",
	NoUnread: "No unread notifications.", NoneOnPage: "No notifications on this page.",
	NoOrganisations: "No organisations.", PageOf: "page %d of %d",
	NotFoundRemoved: "Not found or no longer accessible.", InboxShrank: "The inbox shrank again. R refresh.",
	NoMessage: "No message", UnknownActor: "Unknown actor", NoProject: "No project", UnnamedProject: "Unnamed project",
	NoTicket: "No ticket", UntitledTicket: "Untitled ticket", TimeUnknown: "Time unknown", TicketNumber: "#%d",
	LabelMessage: "Message", LabelActor: "Actor", LabelOrganisation: "Organisation", LabelProject: "Project",
	LabelTicket: "Ticket", LabelEvent: "Event", LabelNotification: "Notification", LabelState: "State",
	LabelLastEvent: "Last event", StateUnread: "unread", StateRead: "read",
	UpDownKey: "up/down", HighlightHelp: "highlight", SelectHelp: "select", OpenHelp: "open inbox", DetailsHelp: "details",
	PageKey: "[/]", PageHelp: "page", PrevPageKey: "[", PrevPageHelp: "prev page", NextPageKey: "]", NextPageHelp: "next page", RefreshKey: "R", RefreshHelp: "refresh", RetryHelp: "retry",
	PageUpDownKey: "pgup/pgdn", ScrollHelp: "scroll", MoveHelp: "move", CloseHelp: "close",
	OrganisationsHelp: "organisations", InboxHelp: "inbox",

	Tickets: "Tickets", Projects: "Projects", Ticket: "Ticket",
	TicketsUnsupported: "Tickets are not supported by this server.",
	TicketUnsupported:  "Ticket details are not supported by this server.",
	NoProjects:         "No projects.", NoTickets: "No tickets.", NoMatches: "No tickets match the search.",
	NoneOnTicketPage: "No tickets on this page.",
	Archived:         "archived", NoStatus: "No status", Unassigned: "Unassigned",
	NoDescription: "No description", NoComments: "No comments", NoAttachments: "No attachments",
	NoText: "No text", Private: "private", UnnamedFile: "Unnamed file",
	CommentsPaged:    "Comments · %s · page %d of %d · %d readable comments",
	CommentsFirst:    "Comments · oldest first · first %d of %d comments",
	CommentsAll:      "Comments · oldest first · %d comments",
	AttachmentsPaged: "Attachments · page %d of %d · %d in all",
	AttachmentsFirst: "Attachments · first %d of %d",
	AttachmentsAll:   "Attachments · %d",
	MetaAssigned:     "assigned %s", MetaUnassigned: "unassigned", MetaCreator: "created by %s", MetaUpdated: "updated %s",
	SearchFor: "search %q", SearchLine: "Search: ", Caret: "_",
	ListShrank:      "The list shrank again. R refresh.",
	OpenTicketsHelp: "open tickets", OpenTicketHelp: "open", TicketsHelp: "tickets", ProjectsHelp: "projects",
	ListHelp: "list", BackHelp: "back",
	SearchKey: "/", SearchHelp: "search", CancelHelp: "cancel", BackspaceKey: "backspace", DeleteHelp: "delete",

	Description: "Description", WholeText: "whole text", NewestFirst: "newest first", OldestFirst: "oldest first",
	TextShortened: "Text shortened by server", TextCut: "Text shortened for this view", CommentUnavailable: "Comment unavailable",
	LoadingWholeText: "Loading the whole text…", LoadingPage: "Loading this page…",
	PageNotLoaded: "This page did not load. R retry.",
	ActiveArea:    "» ", SelectedComment: "> ",
	TypeUnknown: "type unknown", SizeUnknown: "size unknown",
	SizeBytes: "%d B", SizeKiB: "%.1f KiB", SizeMiB: "%.1f MiB", SizeGiB: "%.1f GiB",
	TextAvailable: "text available", TextUnsupported: "text not supported", TextUnavailable: "text unavailable",
	TextEmpty: "no text", TextStatusUnknown: "text status unknown",
	TabKey: "tab", NextAreaHelp: "next section", OrderKey: "o", ExpandKey: "e", ExpandHelp: "expand",
	CollapseHelp: "collapse", CommentHelp: "comment",

	MarkReadKey: "r", MarkReadHelp: "mark read", OpenLinkedTicketHelp: "open ticket", NotificationHelp: "notification",
	LabelMarkRead: "Mark as read",
	Marking:       "Marking as read…", Marked: "Marked as read",
	MarkRefused:           "Not marked: %s",
	MarkNotSent:           "Not marked; the server was not reached. r try again",
	MarkRateLimited:       "Not marked; rate limited. r try again",
	MarkUnknownRefreshing: "Result unknown; refreshing", MarkUnknown: "Result unknown",
	WritePermissionUnknown: "Write permission unknown", ReadOnlyToken: "Read-only token",
	MarkUnsupported: "Marking as read is not supported by this server.",
	MarkingShort:    "marking…", MarkedShort: "marked", NotMarkedShort: "not marked", UnknownShort: "unknown",
	NotificationUnread: "unread", NotificationRead: "read",

	DiskUnavailable:        "Disk cache unavailable; using memory only",
	DiskBusy:               "Disk cache in use by another lalabase; using memory only",
	SessionExpiredDiskKept: "Session expired. Restart lalabase. The disk cache could not be cleared; remove it by hand.",
	// C7: the launch says what came of it, or why nothing opened.
	LaunchKey: "x", LaunchHelp: "Claude Code beside",
	Launching:           "Opening Claude Code beside #%d\u2026",
	LaunchOpened:        "Claude Code opened beside #%d",
	LaunchEnded:         "Claude Code ended; back at #%d",
	LaunchEndedError:    "Claude Code ended with an error: %s",
	LaunchNotHandedOver: "This ticket's address cannot be handed over; nothing opened",
	LaunchNoFolder:      "Folder not found: %s",
	LaunchNotADirectory: "Not a folder: %s",
	LaunchBadFolder:     "This folder cannot be typed into a shell: %s",
	LaunchNoFiles:       "No files for Claude Code: %s",
	LaunchNoClaude:      "claude was not found on the PATH",
	LaunchNoPermission:  "macOS does not let this terminal control iTerm: allow it in System Settings > Privacy & Security > Automation",
	LaunchSessionGone:   "iTerm did not find this session; nothing opened",
	LaunchNoAnswer:      "iTerm did not answer within 5 s; a pane may still open. If macOS asks for permission, allow it",
	LaunchUntracked:     "The state of Claude Code beside #%d cannot be shown: %s",
	AgentLine:           "Agent %s · for %s",
	AgentApproval:       "waiting for approval", AgentWaiting: "waiting for you", AgentLost: "lost",
	AgentWorking: "working", AgentStarting: "starting", AgentEnded: "ended",
	AgeSeconds: "%d s", AgeMinutes: "%d min", AgeHours: "%d h", AgeDays: "%d d",
	LaunchFailed:     "iTerm could not open a pane",
	LaunchTmuxFailed: "tmux could not start Claude Code",
	// L2: the path in the second line, its places and their details.
	PathStep: " › ", PathDetail: " · ",
	// The mark after the name of the token's own person in a comment.
	You: "(you)",
	// The labels of a ticket's meta bar.
	BarStatus: "Status", BarProject: "Project", BarPeople: "People", BarUpdated: "Updated",
	// C9b: the header's summary and the area's title, the key that opens
	// the agents, the meta bar's label of a ticket's agent and the age of
	// its state after its word there.
	Agents: "Agents", AgentsKey: "A", AgentsHelp: "agents",
	BarAgent: "Agent", AgentMeta: "· %s",
	// C9c: the key that removes an agent, and the notices of its result.
	RemoveAgentKey: "d", RemoveAgentHelp: "remove agent",
	AgentRemoved:    "Agent #%d removed.",
	AgentNotRemoved: "Agent #%d could not be removed: %s",
	AgentNotSeen:    "Agent #%d is seen for this run only: %s",
	AgentsTooMany:   "The agents' directory holds too many files to read; the next start cleans it up.",
	// S6: the label of the time the person booked on the ticket.
	BarBooked: "Booked by you", MetaBooked: "booked by you %s",
	// C9c: x on an agent.
	JumpHelp: "jump to agent", AgainHelp: "start agent again",
	AgentHintJump: "x jump to it", AgentHintAgain: "x start again", AgentHintRemove: "d remove",
	Jumping:         "Switching to the agent of #%d\u2026",
	Jumped:          "Switched to the agent of #%d",
	JumpPaneGone:    "The pane of the agent of #%d was not found; it was closed",
	JumpNoPane:      "No pane of the agent of #%d is known in this terminal",
	JumpFailed:      "iTerm did not switch to the agent's pane",
	StartingAgain:   "Starting the agent of #%d again\u2026",
	LaunchResumed:   "Claude Code started again beside #%d",
	AgentLostPane:   "The agent of #%d is lost: its pane is gone",
	AgainNotStarted: "Claude Code did not start again beside #%d; the agent stays",
	AgentNotSettled: "The agents of #%d could not be updated: %s",
	Unlisted:        "(not listed)",
}
