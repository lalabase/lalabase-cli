package ui

import (
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K7c: a meeting's text laid out for reading: speakers as heads, their
// words indented below, a little Markdown, the rest as written.

// laidOutAs is a section laid out under the layout given, as laidOut.
func laidOutAs(raw string, l meetingLayout) []string {
	lines, styles := meetingBody(raw, l)
	return describe(lines, styles)
}

// laidOut is a section laid out, one line each: the text, then its lead
// and style and indent in brackets where it has them.
func laidOut(raw, me string) []string {
	return laidOutAs(raw, meetingLayout{me: me, transcript: true})
}

// describe writes laid out lines as laidOut does.
func describe(lines []string, styles []present.LineStyle) []string {
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
	lines, styles := meetingBody(raw, meetingLayout{transcript: true})
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
		m := fullMeeting(meetingRows(website)[0], "", "", "**"+me+"**\nHallo zusammen.\n**Erika Beispiel**\nGut.")
		m["data"].(map[string]any)["participants"] = []map[string]any{{"display_name": me}, {"display_name": "Erika Beispiel"}}
		return objectAnswer(m)
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

// Review round 1 of #9: a bold line alone is a speaker in the transcript
// alone; in a summary or notes it is a bold line (finding 1). A speaker
// written "**Name:** words" on one line is a head when the name is one of
// the meeting's participants (finding 2). A tab in a bold start keeps the
// bold where it is (finding 3); markers inside an address stay (finding
// 4); a heading ends a turn and loses its own markers (finding 5).
func TestOnlyTheTranscriptHasSpeakers(t *testing.T) {
	t.Parallel()

	summary := "**Themen:**\n- Relaunch\n\n**Entscheidungen:**\n- Start im März"
	want := []string{
		"Themen:|lead Themen: strong",
		"\u2022 Relaunch",
		"",
		"Entscheidungen:|lead Entscheidungen: strong",
		"\u2022 Start im März",
	}
	got := laidOutAs(summary, meetingLayout{me: "Anna Albers"})
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("summary: got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// In the transcript, with participants, a bold line that names none of
	// them is no speaker.
	got = laidOutAs("**Anna Albers**\nHallo.\n**Fazit**\nStart im März.", meetingLayout{transcript: true, speakers: []string{"Anna Albers"}})
	want = []string{
		"Anna Albers|lead Anna Albers author",
		"Hallo.|indent >>",
		"Fazit|lead Fazit strong|indent >>",
		"Start im März.|indent >>",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("transcript: got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestASpeakerOnTheLineOfTheirWordsIsAHead(t *testing.T) {
	t.Parallel()

	raw := "**Anna Ananas:** Ich fasse kurz zusammen.\n**Ben Berger**: Gut.\n**Wichtig:** kein Teilnehmer."
	got := laidOutAs(raw, meetingLayout{transcript: true, me: "Ben Berger", speakers: []string{"Anna Ananas", "ben berger"}})
	want := []string{
		"Anna Ananas|lead Anna Ananas author",
		"Ich fasse kurz zusammen.|indent >>",
		"",
		"Ben Berger|lead Ben Berger own",
		"Gut.|indent >>",
		"Wichtig: kein Teilnehmer.|lead Wichtig: strong|indent >>",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestMarkdownLeavesTabsAddressesAndHeadingsRight(t *testing.T) {
	t.Parallel()

	raw := "**Erg\tebnis:** Rest\nSiehe https://x.example/**a**b und `**c**`.\n**Anna**\nText\n## **Punkt**\nweiter"
	got := laidOutAs(raw, meetingLayout{transcript: true})
	want := []string{
		"Erg    ebnis: Rest|lead Erg    ebnis: strong",
		"Siehe https://x.example/**a**b und `**c**`.",
		"",
		"Anna|lead Anna author",
		"Text|indent >>",
		"Punkt|lead Punkt strong",
		"weiter",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// In the pane the summary has no speakers, and the transcript's speakers
// are the meeting's participants: a bold line naming none of them is bold,
// nothing more.
func TestThePaneGivesSpeakersToTheTranscriptAlone(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.f.setMeeting(func(id string) transport.Answer {
		m := fullMeeting(meetingRows(website)[0], "**Erika Beispiel**\nZusammenfassung.", "", "**Erika Beispiel**\nGut.\n**Fazit**\nStart im März.")
		m["data"].(map[string]any)["participants"] = []map[string]any{{"display_name": "Erika Beispiel"}}
		return objectAnswer(m)
	})
	h.openMeetings(website)
	h.drive(h.press("enter"))
	styleOf := func(line string) present.LineStyle {
		t.Helper()
		c := h.m.objectColumn()
		for i, l := range c.Wrapped {
			if l == line {
				return c.Styles[i]
			}
		}
		t.Fatalf("no line %q in %q", line, c.Wrapped)
		return present.LineStyle{}
	}
	if st := styleOf("Erika Beispiel"); st.LeadStyle != present.Strong {
		t.Fatalf("the summary has a speaker: %+v", st)
	}
	h.press("tab")
	if st := styleOf("Erika Beispiel"); st.LeadStyle != present.Author {
		t.Fatalf("the transcript's participant is no speaker: %+v", st)
	}
	if st := styleOf("Fazit"); st.LeadStyle != present.Strong {
		t.Fatalf("a bold line naming no participant is a speaker: %+v", st)
	}
}
