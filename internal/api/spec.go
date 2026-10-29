// Package api is the client's view of the server: one adapter per MCP tool and
// REST reader it uses, each with a Spec saying what it sends and what it needs
// back. The same Spec drives three checks, so there is one rule, not three:
// feature detection (are the tool and its arguments there?), the runtime
// check of every answer (are the fields there?), and the compatibility test
// against the core's contract.
package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Spec describes one adapter.
type Spec struct {
	// Name identifies the adapter: the tool name, or "GET <path>".
	Name string
	// Tool is the MCP tool; empty for a REST reader.
	Tool string
	// Path is the REST path; empty for a tool.
	Path string
	// Method is the method of a REST write (POST or PATCH); empty for a
	// REST reader and a tool.
	Method string
	// Args are the arguments the adapter may send; the tool's input schema
	// must declare every one of them.
	Args []string
	// Optional are arguments the adapter sends only when asked to, each
	// checked by the feature that asks (features.Feature.ExtraArgs): a server
	// without them keeps the adapter's base feature. The caller asks only
	// when that feature is on.
	Optional []string
	// OneRecord: the tool addresses one record, so an empty answer is "not
	// found" (and an older server's "No ... found for <id>." too).
	OneRecord bool
	// Required are the fields the client relies on, as paths: "a.b" for a
	// field of an object, "list[].c" for a field of every list element.
	// Required means present and not null. A field that may be null is
	// marked "?": "status?" may be null, "ticket?.id" means the ticket may be
	// null, but when it is there it must have an id. A field missing, null
	// without "?", or an object or list in the wrong shape fails the answer.
	Required []string
	// Newer are fields a newer server sends and an older one lacks, as
	// paths like Required: the field the last segment names may be
	// absent; present, it follows the rules of Required (not null without
	// "?", the right shape), and what leads to it must be there. The
	// caller reads an absent one as "the server does not say".
	Newer []string
}

// Specs are all adapters, for the compatibility test and feature detection.
var Specs = []Spec{
	NotificationsSpec, MarkReadSpec, TicketsSpec, TicketSpec, UpdateTicketSpec, CommentSpec, KnowledgeSpec,
	BookedTimeSpec, MeSpec, OrganisationsSpec, ProjectsSpec,
}

// Missing returns the required paths the payload lacks, with list positions
// filled in ("data[3].id"). An error means the payload is no JSON at all.
func Missing(payload []byte, required []string) ([]string, error) {
	return missingOf(payload, required, nil)
}

// Missing checks a payload against the spec's fields: the required ones,
// and the newer ones where they are present.
func (s Spec) Missing(payload []byte) ([]string, error) {
	return missingOf(payload, s.Required, s.Newer)
}

func missingOf(payload []byte, required, newer []string) ([]string, error) {
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("no JSON: %w", err)
	}
	var out []string
	for _, path := range required {
		out = append(out, walk(root, strings.Split(path, "."), "", false)...)
	}
	for _, path := range newer {
		out = append(out, walk(root, strings.Split(path, "."), "", true)...)
	}
	return out, nil
}

// walk follows the remaining path segments from node and returns the paths
// that are absent or null. at is the path walked so far, for the report.
// lastMayBeAbsent: the field the last segment names may be absent (a newer
// field).
func walk(node any, segments []string, at string, lastMayBeAbsent bool) []string {
	if len(segments) == 0 {
		return nil
	}
	seg := segments[0]
	name, isList := strings.CutSuffix(seg, "[]")
	name, nullable := strings.CutSuffix(name, "?")
	here := join(at, name)

	obj, ok := node.(map[string]any)
	if !ok {
		return []string{join(at, seg)}
	}
	value, present := obj[name]
	if !present {
		if lastMayBeAbsent && len(segments) == 1 {
			return nil
		}
		return []string{join(at, seg)}
	}
	if value == nil {
		switch {
		case nullable:
			return nil
		case isList:
			return []string{here + "[]"}
		default:
			return []string{here}
		}
	}
	if !isList {
		return walk(value, segments[1:], here, lastMayBeAbsent)
	}
	list, ok := value.([]any)
	if !ok {
		return []string{here + "[]"}
	}
	var missing []string
	for i, element := range list {
		elementAt := fmt.Sprintf("%s[%d]", here, i)
		if element == nil {
			missing = append(missing, elementAt)
			continue
		}
		missing = append(missing, walk(element, segments[1:], elementAt, lastMayBeAbsent)...)
	}
	return missing
}

func join(at, name string) string {
	if at == "" {
		return name
	}
	return at + "." + name
}
