// Package launch starts Claude Code beside the ticket shown (C7): in a new
// iTerm pane split off the client's own session, in a detached tmux
// session of the client's own socket that one viewer pane shows (tmux.go),
// or, without iTerm, in this terminal while the interface waits. Since C9c
// x also jumps to the pane of an agent that runs (Jump: an AppleScript that
// selects it by the id the launch script returned) and starts an agent
// whose session is over again (Again: a new agent that resumes its latest
// session). Since C10 it also hands the address of a ticket's file, and
// since C12 of a comment's, to the system's opener, for the browser
// (OpenFile).
//
// Threat model. Foreign: everything the server sent (the ids and the number
// are checked against a pattern before use, nothing else of the ticket is
// passed on) and the environment's values (their form is checked).
// Trusted: the person's config file, their shell and Claude Code itself.
// What reaches the command line, the prompt, the launch script and the
// AppleScript is fixed text plus values that passed a pattern or a check of
// their characters (a pane's and a resumed session's id included); the
// AppleScripts are constants and take their values as arguments only.
// What iTerm answers is read from its error's number alone: its texts are
// localised. tmux gets its values as arguments, never through a shell:
// a session's name is an agent's id (agents.ForeignID), the folder and the
// script's path passed passable. tmux's socket lives in its per-user
// directory (0700), which another local user cannot reach.
//
// Which server (C7b). Claude Code is handed the profile's server, with the
// profile's token, in a file of its own (claude --mcp-config, the files in
// internal/launchdir) under a name no server of the person's has, and
// reads no other (--strict-mcp-config): the client never reads Claude
// Code's configuration to guess which server it would take. The token is
// in that file, never in a process's arguments or the typed line. Claude
// Code reads the ticket itself through that server. An injection in the
// ticket can still lead Claude Code to a write with the profile's token:
// accepted, and stated in the plan.
//
// This is the only package that starts a child process (os/exec), named
// one by one in the source guard.
package launch
