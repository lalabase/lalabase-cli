package ui

import (
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K7c: a meeting's text laid out for reading: speakers as heads, their
// words indented below, a little Markdown, the rest as written.

// laidOut is a section laid out, one line each: the text, then its lead
// and style and indent in brackets where it has them.
func laidOut(raw, me string) []string {
	lines, styles := meetingBody(raw, me)
	out := make([]string, len(lines))
	for i, l := range lines {
		st := styles[i]
		out[i] = l
		if st.Lead > 0 {
			out[i] += "|lead " + l[:st.Lead] + " " + map[present.Style]string{present.Author: "author", present.Own: "own", present.Strong: "strong"}[st.LeadStyle]
		}
		if st.Indent > 0 {
			out[i] += "|indent " + strings.Repeat(">", st.Indent)
		}
	}
	return out
}

func TestATranscriptShowsItsSpeakersAsHeads(t *testing.T) {
	t.Parallel()

	raw := "**Samira Barati**\ndann kann nichts ändern\nund Jari hat gefragt.\n**Guido Boyke**\nJa, kann man machen.\n\nNoch ein Absatz.\n**Samira Barati:**\nJa, genau."
	want := []string{
		"Samira Barati|lead Samira Barati author",
		"dann kann nichts ändern|indent >>",
		"und Jari hat gefragt.|indent >>",
		"",
		"Guido Boyke|lead Guido Boyke own",
		"Ja, kann man machen.|indent >>",
		"|indent >>",
		"Noch ein Absatz.|indent >>",
		"",
		"Samira Barati|lead Samira Barati author",
		"Ja, genau.|indent >>",
	}
	if got := laidOut(raw, "Guido Boyke"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Without a name of their own, nobody is the person.
	if got := laidOut("**Guido Boyke**\nJa.", ""); got[0] != "Guido Boyke|lead Guido Boyke author" {
		t.Fatalf("%q", got)
	}
}

func TestASummaryShowsALittleMarkdown(t *testing.T) {
	t.Parallel()

	raw := "## Ergebnis\n**Start:** im März, **nicht** im April.\n- Seiten-Vorlagen\n  - Logo\n* **Texte** fertig\nEin lose ** Stern bleibt.\n#kein Titel"
	want := []string{
		"Ergebnis|lead Ergebnis strong",
		"Start: im März, nicht im April.|lead Start: strong",
		"• Seiten-Vorlagen",
		"• Logo|indent >>",
		"• Texte fertig|lead • Texte strong",
		"Ein lose ** Stern bleibt.",
		"#kein Titel",
	}
	if got := laidOut(raw, ""); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A bold line that is a sentence, or empty markers, is no speaker.
func TestOnlyAShortBoldLineIsASpeaker(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"**" + strings.Repeat("x", maxSpeaker+1) + "**",
		"****",
		"**Anna** und **Ben**",
		"Anna:",
	} {
		if name, ok := speakerOf(line); ok {
			t.Errorf("%q is a speaker %q", line, name)
		}
	}
	for line, want := range map[string]string{"**Anna**": "Anna", "  **Anna:**": "Anna", "**Anna**:": "Anna", "**" + strings.Repeat("y", maxSpeaker) + "**": strings.Repeat("y", maxSpeaker)} {
		if name, ok := speakerOf(line); !ok || name != want {
			t.Errorf("%q: %q %v", line, name, ok)
		}
	}
}

// The text is cleaned before it is laid out: a sequence in a speaker's
// name or words never reaches the lines, and every style's lead stays
// inside its line.
func TestATranscriptIsCleanedBeforeItIsLaidOut(t *testing.T) {
	t.Parallel()

	raw := "**Anna \x1b]52;c;cGF3bmVk\x07Albers**\nWir \x1b[2Jstarten.\r\n**Ben\u202e**\r\nGut."
	lines, styles := meetingBody(raw, "")
	for i, l := range lines {
		if strings.ContainsAny(l, "\x1b\x07\r\u202e") {
			t.Fatalf("line %d %q", i, l)
		}
		if styles[i].Lead > len(l) {
			t.Fatalf("line %d: lead %d past %q", i, styles[i].Lead, l)
		}
	}
	if len(lines) != 5 || !strings.HasPrefix(lines[0], "Anna") || styles[0].LeadStyle != present.Author || styles[4].Indent != indentTurn {
		t.Fatalf("%q %+v", lines, styles)
	}
}

// In the pane a transcript's speakers stand as heads, the person's own
// name in their tone, the words indented below; no marker reaches the
// screen.
func TestATranscriptsSpeakersStandAsHeadsInThePane(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	me := h.m.app.ownName()
	if me == "" {
		t.Fatal("the harness has no name of its own: this test proves nothing then")
	}
	h.f.setMeeting(func(id string) transport.Answer {
		return objectAnswer(fullMeeting(meetingRows(website)[0], "", "", "**"+me+"**\nHallo zusammen.\n**Erika Beispiel**\nGut."))
	})
	h.drive(h.press("enter"))
	c := h.m.objectColumn()
	at := map[string]int{}
	for i, l := range c.Wrapped {
		at[l] = i
	}
	own, other, words := at[me], at["Erika Beispiel"], at["Hallo zusammen."]
	if own == 0 || other == 0 || words == 0 {
		t.Fatalf("lines %q", c.Wrapped)
	}
	if c.Styles[own].LeadStyle != present.Own || c.Styles[other].LeadStyle != present.Author || c.Styles[words].Indent != indentTurn {
		t.Fatalf("styles %+v / %+v / %+v", c.Styles[own], c.Styles[other], c.Styles[words])
	}
	if strings.Contains(h.rendered(), "**") {
		t.Fatal("a marker reached the screen")
	}
}
