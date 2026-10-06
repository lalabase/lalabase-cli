// Package claudecfg finds the Lalabase servers registered in Claude Code's MCP
// configuration, for `lalabase auth import`. It only reads: nothing here runs
// a configured program, expands a variable or guesses at a value. An entry
// that cannot be imported as it stands is listed with the reason.
//
// The three scopes Claude Code keeps servers in:
//
//   - user:    ~/.claude.json, "mcpServers"
//   - local:   ~/.claude.json, "projects" -> <path> -> "mcpServers"
//   - project: .mcp.json in the current directory, "mcpServers"
package claudecfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Entry is one Lalabase server Claude Code knows.
type Entry struct {
	// Scope is user, local or project.
	Scope string
	// Where is the file, or for the local scope the project path.
	Where string
	// Name is the server's name in the configuration.
	Name string
	// BaseURL is the server address without /api/mcp.
	BaseURL string
	// Token is the bearer token; empty when the entry cannot be imported.
	Token string
	// Problem says why the entry cannot be imported; empty when it can.
	Problem string
}

// Usable reports whether the entry can be imported.
func (e Entry) Usable() bool { return e.Problem == "" }

// TokenPrefix shows as much of a token as the server's own pages do.
func TokenPrefix(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:12]
}

type server struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Command string            `json:"command"`
	Headers map[string]string `json:"headers"`
}

var bearer = regexp.MustCompile(`^Bearer (lalab_pat_[A-Za-z0-9]+)$`)

// Find reads the three scopes. A file that is missing is skipped; one that
// cannot be read or parsed is reported in errs and the others are still read.
func Find(home, cwd string) (entries []Entry, errs []error) {
	userFile := filepath.Join(home, ".claude.json")
	var claude struct {
		MCPServers map[string]server `json:"mcpServers"`
		Projects   map[string]struct {
			MCPServers map[string]server `json:"mcpServers"`
		} `json:"projects"`
	}
	if err := readJSON(userFile, &claude); err != nil {
		errs = append(errs, err)
	}
	entries = append(entries, collect("user", userFile, claude.MCPServers)...)
	paths := make([]string, 0, len(claude.Projects))
	for p := range claude.Projects {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		entries = append(entries, collect("local", p, claude.Projects[p].MCPServers)...)
	}

	projectFile := filepath.Join(cwd, ".mcp.json")
	var project struct {
		MCPServers map[string]server `json:"mcpServers"`
	}
	if err := readJSON(projectFile, &project); err != nil {
		errs = append(errs, err)
	}
	entries = append(entries, collect("project", projectFile, project.MCPServers)...)
	return entries, errs
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path) //nolint:gosec // Claude Code's own config files
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func collect(scope, where string, servers map[string]server) []Entry {
	names := make([]string, 0, len(servers))
	for name, s := range servers {
		if isLalabase(name, s) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]Entry, 0, len(names))
	for _, name := range names {
		out = append(out, inspect(scope, where, name, servers[name]))
	}
	return out
}

// isLalabase: named after Lalabase, or addressed like its MCP endpoint, or
// carrying one of its tokens.
func isLalabase(name string, s server) bool {
	if strings.Contains(strings.ToLower(name), "lalabase") {
		return true
	}
	if u, err := url.Parse(s.URL); err == nil && strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/api/mcp") {
		return true
	}
	for _, v := range s.Headers {
		if strings.Contains(v, "lalab_pat_") {
			return true
		}
	}
	return false
}

func inspect(scope, where, name string, s server) Entry {
	e := Entry{Scope: scope, Where: where, Name: name}
	if s.Command != "" || s.Type == "stdio" {
		e.Problem = "runs a local program; nothing to import"
		return e
	}
	base, err := baseURL(s.URL)
	if err != nil {
		e.Problem = err.Error()
		return e
	}
	token, err := bearerToken(s.Headers)
	if err != nil {
		e.Problem = err.Error()
		return e
	}
	e.BaseURL, e.Token = base, token
	return e
}

// baseURL turns the MCP endpoint into the server address:
// https://host/sub/api/mcp becomes https://host/sub. Any other form is
// refused rather than guessed at.
func baseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("unknown address form (want https://<server>/api/mcp)")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("unknown address form: a user, query or fragment in the address")
	}
	path := strings.TrimRight(u.Path, "/")
	base, ok := strings.CutSuffix(path, "/api/mcp")
	if !ok {
		return "", errors.New("unknown address form: the address does not end in /api/mcp")
	}
	u.Path, u.RawPath = base, ""
	return strings.TrimRight(u.String(), "/"), nil
}

// bearerToken reads exactly one Authorization header carrying a Lalabase
// token. A variable is not expanded: its value lives outside this file.
func bearerToken(headers map[string]string) (string, error) {
	var values []string
	for k, v := range headers {
		if strings.EqualFold(k, "Authorization") {
			values = append(values, v)
		}
	}
	switch len(values) {
	case 0:
		return "", errors.New("no Lalabase token: no Authorization header")
	case 1:
	default:
		return "", errors.New("more than one Authorization header; which one counts is unclear")
	}
	if strings.Contains(values[0], "${") {
		return "", errors.New("the token is a variable (${...}); its value lives elsewhere, copy it by hand")
	}
	m := bearer.FindStringSubmatch(values[0])
	if m == nil {
		return "", errors.New("no Lalabase token in the Authorization header")
	}
	return m[1], nil
}
