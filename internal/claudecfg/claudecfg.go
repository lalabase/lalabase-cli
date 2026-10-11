// Package claudecfg finds the Lalabase servers registered in Claude Code's MCP
// configuration: for `lalabase auth import` (Find), and for the launch of
// Claude Code beside a ticket (Visible, C7). It only reads: nothing here runs
// a configured program, expands a variable or guesses at a value. An entry
// that cannot be imported as it stands is listed with the reason.
//
// The three scopes Claude Code keeps servers in:
//
//   - user:    ~/.claude.json, "mcpServers"
//   - local:   ~/.claude.json, "projects" -> <path> -> "mcpServers"
//   - project: .mcp.json in the current directory, "mcpServers"
//
// A .mcp.json is foreign: a cloned repository brings its own.
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
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gboyke/lalabase-cli/internal/safefile"
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
	// Note is shown with the entry: for the project scope, that it comes from
	// the current directory's .mcp.json (a cloned repository's, perhaps).
	Note string
	// Approval is shown with a project entry: what ~/.claude.json records for
	// it, and only that. Claude Code's effective settings (its settings files,
	// managed settings) may decide otherwise; they are not read.
	Approval string
}

// Limits on what is read. Claude Code's own file keeps a history and can
// grow; a project's .mcp.json is a few lines.
const (
	maxUserFile    = 64 << 20
	maxProjectFile = 1 << 20
)

// Usable reports whether the entry can be imported.
func (e Entry) Usable() bool { return e.Problem == "" }

// TokenPrefix shows as much of a token as the server's own pages do.
func TokenPrefix(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:12]
}

// server is one MCP server as Claude Code's configuration holds it. bad
// says why it could not be read; such a server stays in the list as an
// entry with a problem, since it may be the one that shadows a name.
type server struct {
	Type    string
	URL     string
	Command string
	Headers map[string]string
	bad     string
}

var bearer = regexp.MustCompile(`^Bearer (lalab_pat_[A-Za-z0-9]+)$`)

// object is a JSON object read with its keys as written. Go's encoding/json
// matches a struct field without regard to case, Claude Code (JavaScript)
// does not: with struct tags, a file holding both "url" and "URL" would
// show this client one address and Claude Code another (review round 1 of
// C7). Every key here is looked up exactly.
type object map[string]json.RawMessage

// field decodes the value of key into out; a missing key leaves out as it
// is.
func (o object) field(key string, out any) error {
	raw, ok := o[key]
	if !ok {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// UnmarshalJSON never fails: what is not a server of the expected shape is
// a server with a problem.
func (s *server) UnmarshalJSON(data []byte) error {
	var o object
	if err := json.Unmarshal(data, &o); err != nil || o == nil {
		s.bad = "not an object"
		return nil //nolint:nilerr // recorded in s.bad: the server stays as an entry with a problem
	}
	for key, out := range map[string]any{"type": &s.Type, "url": &s.URL, "command": &s.Command, "headers": &s.Headers} {
		if err := o.field(key, out); err != nil {
			s.bad = "the field " + key + " has another type than expected"
		}
	}
	return nil
}

// userConfig is what ~/.claude.json says about MCP servers.
type userConfig struct {
	MCPServers map[string]server
	Projects   map[string]projectEntry
}

func (c *userConfig) UnmarshalJSON(data []byte) error {
	var o object
	if err := json.Unmarshal(data, &o); err != nil {
		return err
	}
	if err := o.field("mcpServers", &c.MCPServers); err != nil {
		return err
	}
	return o.field("projects", &c.Projects)
}

// projectEntry is one project of ~/.claude.json: its local servers and
// what it records about the approval of a .mcp.json.
type projectEntry struct {
	MCPServers                 map[string]server
	EnabledMcpjsonServers      []string
	DisabledMcpjsonServers     []string
	EnableAllProjectMcpServers bool
}

func (p *projectEntry) UnmarshalJSON(data []byte) error {
	var o object
	if err := json.Unmarshal(data, &o); err != nil {
		return err
	}
	for key, out := range map[string]any{
		"mcpServers": &p.MCPServers, "enabledMcpjsonServers": &p.EnabledMcpjsonServers,
		"disabledMcpjsonServers": &p.DisabledMcpjsonServers, "enableAllProjectMcpServers": &p.EnableAllProjectMcpServers,
	} {
		if err := o.field(key, out); err != nil {
			return err
		}
	}
	return nil
}

type projectConfig struct {
	MCPServers map[string]server
}

func (c *projectConfig) UnmarshalJSON(data []byte) error {
	var o object
	if err := json.Unmarshal(data, &o); err != nil {
		return err
	}
	return o.field("mcpServers", &c.MCPServers)
}

// Find reads the three scopes. A file that is missing is skipped; one that
// cannot be read or parsed is reported in errs and the others are still read.
func Find(home, cwd string) (entries []Entry, errs []error) {
	userFile := filepath.Join(home, ".claude.json")
	var claude userConfig
	if err := readJSON(userFile, maxUserFile, &claude); err != nil {
		errs = append(errs, err)
	}
	entries = append(entries, collect("user", userFile, claude.MCPServers, isLalabase)...)
	paths := make([]string, 0, len(claude.Projects))
	for p := range claude.Projects {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		entries = append(entries, collect("local", p, claude.Projects[p].MCPServers, isLalabase)...)
	}

	projectFile := filepath.Join(cwd, ".mcp.json")
	var project projectConfig
	if err := readJSON(projectFile, maxProjectFile, &project); err != nil {
		errs = append(errs, err)
	}
	// Claude Code keeps a project under its real path; the directory may
	// have been entered through a link.
	approval, ok := claude.Projects[cwd]
	if real, err := filepath.EvalSymlinks(cwd); !ok && err == nil {
		approval = claude.Projects[real]
	}
	for _, e := range collect("project", projectFile, project.MCPServers, isLalabase) {
		state := "none"
		switch {
		case slices.Contains(approval.DisabledMcpjsonServers, e.Name):
			state = "rejected"
		case approval.EnableAllProjectMcpServers || slices.Contains(approval.EnabledMcpjsonServers, e.Name):
			state = "approved"
		}
		e.Note = "from .mcp.json in this directory (a cloned repository's, perhaps): check the address"
		e.Approval = "approval entry in ~/.claude.json: " + state + ". Effective Claude Code settings were not checked."
		entries = append(entries, e)
	}
	return entries, errs
}

func readJSON(path string, maxBytes int64, out any) error {
	data, _, err := safefile.ReadRegular(path, maxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Bytes that are not UTF-8 decode to one U+FFFD each here and to one
	// per broken sequence in JavaScript (review round 3 of C7).
	if !utf8.Valid(data) {
		return fmt.Errorf("%s: not UTF-8; Claude Code reads it differently", path)
	}
	if loneSurrogate(data) {
		return fmt.Errorf("%s: a lone UTF-16 surrogate escape; Claude Code reads it differently", path)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// loneSurrogate reports a \u escape of a UTF-16 surrogate without its
// partner in a JSON string. Go decodes one as U+FFFD, JavaScript keeps it:
// two names distinct for Claude Code could become one here and hide a
// server (review round 2 of C7). A file holding one is refused.
func loneSurrogate(data []byte) bool {
	in := false
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case !in:
			in = c == '"'
		case c == '"':
			in = false
		case c == '\\' && i+1 < len(data) && data[i+1] == 'u':
			r, ok := hex4(data, i+2)
			if !ok {
				return false // malformed: json.Unmarshal refuses it
			}
			i += 5
			switch {
			case 0xD800 <= r && r < 0xDC00:
				next, ok := hex4(data, i+3)
				if i+2 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' || !ok || next < 0xDC00 || next > 0xDFFF {
					return true
				}
				i += 6
			case 0xDC00 <= r && r <= 0xDFFF:
				return true
			}
		case c == '\\':
			i++ // the escaped character, a backslash or a quote among them
		}
	}
	return false
}

// hex4 reads four hex digits at data[at:].
func hex4(data []byte, at int) (rune, bool) {
	if at+4 > len(data) {
		return 0, false
	}
	var r rune
	for _, c := range data[at : at+4] {
		switch {
		case '0' <= c && c <= '9':
			r = r<<4 | rune(c-'0')
		case 'a' <= c && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case 'A' <= c && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

// Visible lists every server Claude Code may use when it starts in folder,
// whatever its name and whether or not it is a Lalabase server, in the
// order user, local, project:
//
//   - user: every server of every global file Claude Code may read
//     (globalFiles: under home and configDir, the legacy file too);
//   - local: the servers of those files filed under a project key that
//     equals one of the folder's ProjectKeys without regard to case (a file
//     system without case takes either spelling);
//   - project: every .mcp.json in the folder and each directory above it,
//     on the path as given and on the path with its links resolved (Claude
//     Code walks the physical one), nearest first.
//
// It lists more than Claude Code may use, never less: the launch asks of a
// name that every server so called points at one place, and a server listed
// in vain can only stop a launch, never send one elsewhere. A file that
// cannot be read is reported in errs: what it holds is unknown. configDir
// is CLAUDE_CONFIG_DIR, or "" when it is not set.
func Visible(home, configDir, folder string) (entries []Entry, errs []error) {
	keys := ProjectKeys(folder)
	for _, file := range globalFiles(home, configDir) {
		var claude userConfig
		if err := readJSON(file, maxUserFile, &claude); err != nil {
			errs = append(errs, err)
		}
		entries = append(entries, collect("user", file, claude.MCPServers, anyServer)...)
		paths := make([]string, 0, len(claude.Projects))
		for p := range claude.Projects {
			if slices.ContainsFunc(keys, func(k string) bool { return strings.EqualFold(k, p) }) {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		for _, p := range paths {
			entries = append(entries, collect("local", p, claude.Projects[p].MCPServers, anyServer)...)
		}
	}
	var seen []string
	starts := []string{filepath.Clean(folder)}
	if real, err := filepath.EvalSymlinks(folder); err == nil {
		starts = append(starts, real)
	}
	for _, start := range starts {
		for dir := start; ; {
			file := filepath.Join(dir, ".mcp.json")
			if !slices.Contains(seen, file) {
				seen = append(seen, file)
				var project projectConfig
				if err := readJSON(file, maxProjectFile, &project); err != nil {
					errs = append(errs, err)
				}
				entries = append(entries, collect("project", file, project.MCPServers, anyServer)...)
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return entries, errs
}

// globalFiles are the files Claude Code may read its global configuration
// from (read in Claude Code 2.1.296): .claude.json under CLAUDE_CONFIG_DIR
// or else the home, with a suffix for another login (-custom-oauth,
// -local-oauth, -staging-oauth), and the legacy .config.json under
// CLAUDE_CONFIG_DIR or ~/.claude. All of them are read: which one Claude
// Code takes depends on its environment in the new pane.
func globalFiles(home, configDir string) []string {
	var out []string
	for _, dir := range []string{home, configDir} {
		if dir == "" {
			continue
		}
		for _, suffix := range []string{"", "-custom-oauth", "-local-oauth", "-staging-oauth"} {
			out = append(out, filepath.Join(dir, ".claude"+suffix+".json"))
		}
	}
	for _, dir := range []string{filepath.Join(home, ".claude"), configDir} {
		if dir != "" {
			out = append(out, filepath.Join(dir, ".config.json"))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ProjectKeys are the paths Claude Code may file a folder's local servers
// under. Claude Code keys a project by the root of the main checkout when
// the folder lies in a git repository (a worktree's too), else by the folder
// itself. Every candidate is returned: the folder as given and resolved, its
// git root, and for a worktree the main checkout's root. Reading more keys
// than Claude Code uses can only stop a launch.
func ProjectKeys(folder string) []string {
	folder = filepath.Clean(folder)
	keys := []string{folder}
	add := func(p string) {
		if !slices.Contains(keys, p) {
			keys = append(keys, p)
		}
	}
	real, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return keys
	}
	add(real)
	for dir := real; ; {
		// Any .git marks a root: a directory, a worktree's file, a link
		// (review round 2 of C7).
		info, err := os.Lstat(filepath.Join(dir, ".git"))
		if err == nil {
			add(dir)
			if !info.IsDir() {
				if main, ok := mainCheckout(dir); ok {
					add(main)
				}
			}
			return keys
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return keys
		}
		dir = parent
	}
}

// mainCheckout follows a worktree's .git file ("gitdir: <dir>") to the
// common directory its git directory names ("commondir") and returns the
// checkout that common directory belongs to, or for a bare repository the
// repository itself, as Claude Code keys it. A .git file without a common
// directory (a submodule) has no main checkout.
func mainCheckout(root string) (string, bool) {
	gitdir, ok := pointer(filepath.Join(root, ".git"), "gitdir: ", root)
	if !ok {
		return "", false
	}
	common, ok := pointer(filepath.Join(gitdir, "commondir"), "", gitdir)
	if !ok {
		return "", false
	}
	if filepath.Base(common) == ".git" {
		common = filepath.Dir(common)
	}
	main, err := filepath.EvalSymlinks(common)
	if err != nil {
		return "", false
	}
	return main, true
}

// pointer reads a one-line git pointer file: the path after prefix,
// relative to base unless absolute.
func pointer(file, prefix, base string) (string, bool) {
	data, _, err := safefile.ReadRegular(file, 4096)
	if err != nil {
		return "", false
	}
	line, ok := strings.CutPrefix(strings.TrimRight(string(data), "\r\n"), prefix)
	if !ok || line == "" || strings.ContainsAny(line, "\r\n") {
		return "", false
	}
	if !filepath.IsAbs(line) {
		line = filepath.Join(base, line)
	}
	return filepath.Clean(line), true
}

func anyServer(string, server) bool { return true }

func collect(scope, where string, servers map[string]server, take func(string, server) bool) []Entry {
	names := make([]string, 0, len(servers))
	for name, s := range servers {
		if take(name, s) {
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
	if s.bad != "" {
		e.Problem = "unreadable entry: " + s.bad
		return e
	}
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
	// An escape in the path is decoded here and kept in Claude Code's
	// request (/tenant%2Fapi/mcp is not /tenant/api/mcp).
	if strings.Contains(u.EscapedPath(), "%") {
		return "", errors.New("unknown address form: an escaped character in the path")
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
