package uitext

import "github.com/gboyke/lalabase-cli/internal/present"

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
	count
)

func Inline(id ID) present.Inline { return present.NewInline(english[id]) }
func Body(id ID) present.Body     { return present.NewBody(english[id]) }

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
}
