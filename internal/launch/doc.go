// Package launch starts Claude Code beside the ticket shown (C7): in a new
// iTerm pane split off the client's own session, or, without iTerm, in this
// terminal while the interface waits.
//
// Threat model. Foreign: everything the server sent (the ids and the number
// are checked against a pattern before use, nothing else of the ticket is
// passed on), the names and addresses in a project's .mcp.json (a cloned
// repository's), and the environment's values (their form is checked).
// Trusted: the person's config file, their shell and Claude Code itself.
// What reaches the command line, the prompt and the AppleScript is fixed
// text plus values that passed a pattern or a check of their characters;
// the AppleScript is a constant and takes its values as arguments only.
// Claude Code reads the ticket itself, through its own MCP server, and only
// a server whose every entry of that name points at the profile's server is
// named (pickServer). An injection in the ticket can still lead Claude Code
// to a write with its own token: accepted, and stated in the plan.
//
// This is the only package that starts a child process (os/exec), named
// one by one in the source guard.
package launch
