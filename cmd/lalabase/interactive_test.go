package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/profile"
)

func TestNoCommandRequiresBothTerminals(t *testing.T) {
	for _, in := range []bool{false, true} {
		for _, out := range []bool{false, true} {
			var stdout, stderr bytes.Buffer
			launched := false
			env := Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
				Interactive: in, OutputTerminal: out, Getenv: func(k string) string {
					return map[string]string{"LC_ALL": "UTF-8", "LALABASE_URL": "http://127.0.0.1:1", "LALABASE_TOKEN": "dummy"}[k]
				},
				runUI: func(context.Context, io.Reader, io.Writer, []string) error { launched = true; return nil }}
			code := runWith(nil, env)
			if in && out {
				if code != 0 || !launched {
					t.Fatalf("both terminals: exit %d, launched %v", code, launched)
				}
			} else if code != 2 || launched || !strings.Contains(stderr.String(), "stdin and stdout") {
				t.Fatalf("stdin %v stdout %v: exit %d, launched %v, stderr %q", in, out, code, launched, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatal("preflight wrote stdout")
			}
		}
	}
}

func TestExistingCommandsBypassUIPreflight(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"version"}, {"auth"}, {"nonsense"}} {
		var out, errOut bytes.Buffer
		env := Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { t.Fatal("command entered UI preflight"); return "" }}
		code := runWith(args, env)
		if args[0] == "help" || args[0] == "--help" {
			if code != 0 || !strings.Contains(out.String(), "lalabase [--profile default]\n") {
				t.Fatalf("help lacks interactive invocation: exit %d, %q", code, out.String())
			}
		} else if args[0] == "version" && code != 0 {
			t.Fatalf("version: exit %d", code)
		} else if args[0] != "version" && code != 2 {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
}

func TestInteractiveStartsWithoutRequests(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusTeapot) }))
	defer srv.Close()
	env, errOut := interactiveEnv(t, map[string]string{"LALABASE_URL": srv.URL, "LALABASE_TOKEN": "dummy"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	env.Context, env.runUI, env.Stdin = ctx, nil, strings.NewReader("q")
	var out bytes.Buffer
	env.Stdout = &out
	code := runInteractive("", env)
	// These buffers have no terminal dimensions. The renderer's paste mode
	// proves that the real runner started; PTY tests check the visible frame.
	if code != 0 || !strings.Contains(out.String(), "\x1b[?2004h") {
		t.Fatalf("runner not started: exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	if requests.Load() != 0 {
		t.Fatalf("startup made %d requests", requests.Load())
	}
	if ctx.Err() != nil {
		t.Fatal("startup exceeded its deadline")
	}
}

func TestInteractiveProfileFailureBeforeUI(t *testing.T) {
	for _, kind := range []string{"missing", "incomplete", "insecure", "agent-only"} {
		t.Run(kind, func(t *testing.T) {
			env, errOut := interactiveEnv(t, nil)
			path := filepath.Join(env.Home, ".config", "lalabase", "config.toml")
			if kind != "missing" {
				name := "default"
				if kind == "agent-only" {
					name = "agent"
				}
				entry := profile.Entry{URL: "http://127.0.0.1:1", Token: "dummy-secret"}
				if kind == "incomplete" {
					entry.Token = ""
				}
				if err := profile.Save(path, profile.File{Profiles: map[string]profile.Entry{name: entry}}); err != nil {
					t.Fatal(err)
				}
				if kind == "insecure" {
					if err := os.Chmod(path, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			env.runUI = func(context.Context, io.Reader, io.Writer, []string) error {
				t.Fatal("profile failure started the UI")
				return nil
			}
			if code := runInteractive("", env); code != 1 || !strings.Contains(errOut.String(), "lalabase auth import") {
				t.Fatalf("exit %d: %s", code, errOut.String())
			}
			if strings.Contains(errOut.String(), "dummy-secret") {
				t.Fatal("diagnostic leaked token")
			}
		})
	}
}

func interactiveEnv(t *testing.T, vars map[string]string) (Env, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	return Env{Home: t.TempDir(), Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut,
		Interactive: true, OutputTerminal: true,
		Getenv: func(k string) string {
			if k == "LC_ALL" {
				return "UTF-8"
			}
			if strings.HasPrefix(k, "LALABASE_AGENT") {
				t.Fatalf("read agent variable %s", k)
			}
			return vars[k]
		},
		runUI: func(context.Context, io.Reader, io.Writer, []string) error { return nil }}, &errOut
}

func TestInteractiveDefaultResolution(t *testing.T) {
	env, errOut := interactiveEnv(t, map[string]string{"LALABASE_URL": "http://127.0.0.1:1", "LALABASE_TOKEN": "dummy"})
	if code := runInteractive("default", env); code != 1 {
		t.Fatalf("explicit default must require the file: exit %d", code)
	}
	errOut.Reset()
	if code := runInteractive("", env); code != 0 {
		t.Fatalf("complete env pair: exit %d: %s", code, errOut.String())
	}
	path := filepath.Join(env.Home, ".config", "lalabase", "config.toml")
	if err := profile.Save(path, profile.File{Profiles: map[string]profile.Entry{"default": {URL: "http://127.0.0.1:2", Token: "file-dummy"}}}); err != nil {
		t.Fatal(err)
	}
	env.Getenv = func(k string) string {
		return map[string]string{"LC_ALL": "UTF-8", "LALABASE_URL": "http://127.0.0.1:1"}[k]
	}
	if code := runInteractive("", env); code != 1 {
		t.Fatalf("partial env pair: exit %d", code)
	}
	if code := runInteractive("default", env); code != 0 {
		t.Fatalf("explicit default must ignore partial pair: exit %d", code)
	}
	env.Getenv = func(k string) string {
		if k == "LC_ALL" {
			return "UTF-8"
		}
		return ""
	}
	if code := runInteractive("", env); code != 0 {
		t.Fatalf("implicit file default: exit %d", code)
	}
}

func TestInteractivePreflightOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		input, output bool
		locale        string
		calls         int
	}{
		{"agent", true, true, "UTF-8", 0},
		{"", false, true, "UTF-8", 0},
		{"", true, false, "UTF-8", 0},
		{"", true, true, "C", 1},
	} {
		var out, errOut bytes.Buffer
		calls := 0
		env := Env{Stdout: &out, Stderr: &errOut, Interactive: tc.input, OutputTerminal: tc.output,
			Getenv: func(k string) string {
				calls++
				if k != "LC_ALL" {
					t.Fatalf("read %s before locale accepted", k)
				}
				return tc.locale
			},
			runUI: func(context.Context, io.Reader, io.Writer, []string) error {
				t.Fatal("launched UI before preflight passed")
				return nil
			}}
		if code := runInteractive(tc.name, env); code != 2 || calls != tc.calls {
			t.Fatalf("%+v: exit %d, calls %d", tc, code, calls)
		}
		if out.Len() != 0 || strings.ContainsAny(errOut.String(), "\x1b\x07") {
			t.Fatal("preflight emitted terminal controls")
		}
	}
}

func TestInteractiveRejectsNonDefaultProfile(t *testing.T) {
	for _, name := range []string{"agent", "work", "bad\x1b[31m"} {
		var out, errOut bytes.Buffer
		env := Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { t.Fatal("profile flag rejection read environment"); return "" }}
		if code := runWith([]string{"--profile", name}, env); code != 2 || !strings.Contains(errOut.String(), "only --profile default") {
			t.Fatalf("profile %q: exit %d, stderr %q", name, code, errOut.String())
		}
		if out.Len() != 0 || strings.Contains(errOut.String(), "\x1b") {
			t.Fatal("unsafe preflight output")
		}
	}
}

func TestInvalidGlobalFlagIsASCIIBeforePreflight(t *testing.T) {
	for _, name := range []string{"--bad\x1b[31m", "--bad\u009b", "--bad\u202e", "--bad\xff", "--bad\u00db", "--profile"} {
		var out, errOut bytes.Buffer
		env := Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { t.Fatal("flag error read environment"); return "" }}
		if code := runWith([]string{name}, env); code != 2 || out.Len() != 0 {
			t.Fatalf("exit %d, stdout %q", code, out.String())
		}
		for _, b := range errOut.Bytes() {
			if b != '\n' && (b < 32 || b >= 127) {
				t.Fatalf("unsafe preflight flag diagnostic: %q", errOut.String())
			}
		}
	}
}

func TestUnknownCommandIsASCIIBeforePreflight(t *testing.T) {
	for _, name := range []string{"bad\u00db", "bad\x1b[31m", "bad\u009b", "bad\u202e", "bad\xff"} {
		var out, errOut bytes.Buffer
		env := Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { t.Fatal("unknown command read environment"); return "" }}
		if code := runWith([]string{name}, env); code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "unknown command") {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
		}
		for _, b := range errOut.Bytes() {
			if b != '\n' && (b < 32 || b >= 127) {
				t.Fatalf("unsafe command diagnostic: %q", errOut.String())
			}
		}
	}
}

func TestInteractivePassesOnlyTerminalEnvironment(t *testing.T) {
	allowed := []string{"TERM", "TERM_PROGRAM", "COLORTERM", "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "SSH_TTY", "WT_SESSION"}
	for _, populated := range []bool{false, true} {
		vars := map[string]string{"LALABASE_URL": "http://127.0.0.1:1", "LALABASE_TOKEN": "dummy-secret", "TMUX": "dummy", "TEA_DEBUG": "1", "TEA_TRACE": "dummy-trace", "UV_DEBUG": "dummy-debug"}
		want := []string{}
		if populated {
			for _, key := range allowed {
				vars[key] = "test-" + key
				want = append(want, key+"="+vars[key])
			}
		}
		env, _ := interactiveEnv(t, vars)
		called := false
		env.runUI = func(ctx context.Context, in io.Reader, out io.Writer, environment []string) error {
			called = true
			if ctx == nil || in != env.Stdin || out != env.Stdout {
				t.Fatal("runner lost context or terminal streams")
			}
			if !reflect.DeepEqual(environment, want) {
				t.Errorf("runner environment %q, want %q", environment, want)
			}
			return nil
		}
		if code := runInteractive("", env); code != 0 || !called {
			t.Fatalf("exit %d, called %v", code, called)
		}
	}
}
