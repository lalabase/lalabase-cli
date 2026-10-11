package launch

import (
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/gboyke/lalabase-cli/internal/claudecfg"
)

// serverName is what an MCP server's name may be to go into the prompt:
// the name comes from Claude Code's configuration, a project's .mcp.json
// among it, and a cloned repository's file is not ours.
var serverName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// scopeRank is Claude Code's precedence between servers of one name: local
// before project before user.
var scopeRank = map[string]int{"local": 0, "project": 1, "user": 2}

// pickServer chooses the name of the MCP server Claude Code reads the
// profile's server through, from every server Claude Code may use in the
// folder (claudecfg.Visible).
//
// The rule: a name counts when every server of that name points at the
// profile's server. Which of them Claude Code then takes (its precedence,
// approvals, settings this client does not read) cannot send it elsewhere.
// A name that also points elsewhere, or at something unreadable, is never
// used. Among the names that count, Claude Code's own precedence picks:
// the best scope a name has, then the name; a name outside serverName is
// passed over. kind is OK when a name was found; otherwise detail says
// what the status line names.
func pickServer(entries []claudecfg.Entry, profile string) (name string, kind Kind, detail string) {
	byName := map[string][]claudecfg.Entry{}
	for _, e := range entries {
		byName[e.Name] = append(byName[e.Name], e)
	}
	type candidate struct {
		name string
		rank int
	}
	var fits []candidate
	conflict := ""
	for n, group := range byName {
		matching, rank := 0, len(scopeRank)
		var other claudecfg.Entry
		for _, e := range group {
			if e.BaseURL != "" && sameServer(e.BaseURL, profile) {
				matching++
				rank = min(rank, scopeRank[e.Scope])
			} else if other.Name == "" {
				other = e
			}
		}
		switch {
		case matching == len(group):
			fits = append(fits, candidate{n, rank})
		case matching > 0 && (conflict == "" || n < conflict):
			conflict = n
			detail = n + "; " + elsewhere(other)
		}
	}
	slices.SortFunc(fits, func(a, b candidate) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		return strings.Compare(a.name, b.name)
	})
	for _, c := range fits {
		if serverName.MatchString(c.name) {
			return c.name, OK, ""
		}
	}
	switch {
	case len(fits) > 0:
		return "", BadName, fits[0].name
	case conflict != "":
		return "", Conflict, detail
	}
	var found []string
	for _, e := range entries {
		if e.BaseURL != "" && !slices.Contains(found, e.BaseURL) {
			found = append(found, e.BaseURL)
		}
	}
	has := "none"
	if len(found) > 0 {
		slices.Sort(found)
		has = strings.Join(found, ", ")
	}
	return "", NoServer, profile + "; Claude Code has: " + has
}

// elsewhere names where a server of a conflicting name points: its address,
// or why it has none.
func elsewhere(e claudecfg.Entry) string {
	if e.BaseURL != "" {
		return e.BaseURL
	}
	return e.Scope + " " + e.Where + ": " + e.Problem
}

// sameServer: two base addresses name the same server. Scheme and host are
// compared without case, a default port is the same as none, a trailing
// slash is dropped; the path is compared as it is. An address with a user,
// query or fragment, or not over http(s), is the same as nothing.
func sameServer(a, b string) bool {
	na, okA := normalBase(a)
	nb, okB := normalBase(b)
	return okA && okB && na == nb
}

func normalBase(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", false
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" || strings.Contains(host, ":") {
		host = net.JoinHostPort(host, port)
		host = strings.TrimSuffix(host, ":")
	}
	return scheme + "://" + host + strings.TrimRight(u.EscapedPath(), "/"), true
}
