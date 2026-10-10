// Package uitext holds every text of the interface, in English, in one
// place. A text reaches the screen only as a present value, so a mislabelled
// foreign string in the catalogue could still not bypass the cleaning.
package uitext

import (
	"fmt"

	"github.com/gboyke/lalabase-cli/internal/present"
)

type ID uint8

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
	UTC
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
	LabelStatus
	LabelAssignees
	LabelCreator
	LabelUpdated
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
	RateLimited:          "Rate limited until %s UTC",
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
	LabelLastEvent: "Last event", StateUnread: "unread", StateRead: "read", UTC: "UTC",
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
	LabelStatus:      "Status", LabelAssignees: "Assignees", LabelCreator: "Creator", LabelUpdated: "Updated",
	SearchFor: "search %q", SearchLine: "Search: ", Caret: "_",
	ListShrank:      "The list shrank again. R refresh.",
	OpenTicketsHelp: "open tickets", OpenTicketHelp: "open", TicketsHelp: "tickets", ProjectsHelp: "projects",
	ListHelp: "list", BackHelp: "back",
	SearchKey: "/", SearchHelp: "search", CancelHelp: "cancel", BackspaceKey: "backspace", DeleteHelp: "delete",

	Description: "Description", WholeText: "whole text", NewestFirst: "newest first", OldestFirst: "oldest first",
	TextShortened: "Text shortened by server", CommentUnavailable: "Comment unavailable",
	LoadingWholeText: "Loading the whole text…", LoadingPage: "Loading this page…",
	PageNotLoaded: "This page did not load. R retry.",
	ActiveArea:    "» ", SelectedComment: "> ",
	TypeUnknown: "type unknown", SizeUnknown: "size unknown",
	SizeBytes: "%d B", SizeKiB: "%.1f KiB", SizeMiB: "%.1f MiB", SizeGiB: "%.1f GiB",
	TextAvailable: "text available", TextUnsupported: "text not supported", TextUnavailable: "text unavailable",
	TextEmpty: "no text", TextStatusUnknown: "text status unknown",
	TabKey: "tab", NextAreaHelp: "next section", OrderKey: "o", ExpandKey: "e", ExpandHelp: "expand",
	CollapseHelp: "collapse", CommentHelp: "comment",
}
