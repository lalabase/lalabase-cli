package sourceguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// C1e: the three packages that touch the file system for the interface
// (internal/diskcache, internal/privatefs, internal/safefile) import what
// fileSystemImports names, by name, and nothing more; the output and
// goroutine rules hold there as everywhere the interface reaches; no other
// reachable package gets their imports; the views cannot reach them.

// reach makes the three packages reachable, as the root controller does
// through the disk cache.
const reach = `package ui; import ( _ "` + clientModule + `internal/diskcache"; _ "` + clientModule + `internal/gateway" )`

var fsDeps = map[string]string{
	"internal/ui/disk.go":             reach,
	"internal/diskcache/diskcache.go": `package diskcache; import ( _ "` + clientModule + `internal/privatefs"; _ "` + clientModule + `internal/safefile" )`,
	"internal/privatefs/privatefs.go": `package privatefs`,
	"internal/safefile/safefile.go":   `package safefile`,
	"internal/gateway/gateway.go":     `package gateway`,
}

func withFS(extra map[string]string) map[string]string {
	files := map[string]string{}
	for k, v := range fsDeps {
		files[k] = v
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func TestTheFileSystemPackagesImportByName(t *testing.T) {
	// What they do is allowed.
	for name, source := range map[string]string{
		"internal/diskcache/x.go":          `package diskcache; import ("os"; "io/fs"; "path/filepath"; "crypto/sha256"; "encoding/hex"); var _ = os.CreateTemp; var _ = os.Rename; var _ = os.Remove; var _ = os.MkdirAll; var _ = os.Chtimes; var _ = os.Open; var _ *os.File; var _ fs.FileInfo; var _ = filepath.Join; var _ = sha256.Sum256; var _ = hex.EncodeToString`,
		"internal/privatefs/x.go":          `package privatefs; import ("os"; "syscall"; "io/fs"; "path/filepath"); var _ = os.OpenFile; var _ = os.Getuid; var _ = os.Lstat; var _ = os.Readlink; var _ = syscall.Flock; var _ = syscall.LOCK_NB; var _ syscall.Stat_t; var _ fs.FileMode; var _ = filepath.Abs`,
		"internal/privatefs/acl_darwin.go": `package privatefs; import ("unsafe"; "encoding/binary"; "golang.org/x/sys/unix"); func f(p *byte) { unix.Syscall6(unix.SYS_GETATTRLIST, uintptr(unsafe.Pointer(p)), 0, 0, 0, 0, 0) }; var _ = unix.BytePtrFromString; var _ = binary.NativeEndian`,
		"internal/safefile/x.go":           `package safefile; import ("os"; "syscall"; "io/fs"); var _ = os.OpenFile; var _ = os.O_RDONLY; var _ = syscall.O_NONBLOCK; var _ = syscall.O_NOFOLLOW; var _ fs.FileInfo`,
	} {
		if got := inspectUI(withFS(map[string]string{name: source})); len(got) != 0 {
			t.Errorf("%s rejected: %v", name, got)
		}
	}
	// What they do not do is refused: an output stream, the environment,
	// the process, a goroutine, logging, a name outside the list, an
	// import outside the list, and x/sys/unix beyond its three names.
	for name, source := range map[string]string{
		"diskcache#stdout":    `package diskcache; import "os"; var out = os.Stdout`,
		"diskcache#stderr":    `package diskcache; import sys "os"; var out = sys.Stderr`,
		"diskcache#exit":      `package diskcache; import "os"; var quit = os.Exit`,
		"diskcache#getenv":    `package diskcache; import "os"; var env = os.Getenv`,
		"diskcache#environ":   `package diskcache; import "os"; var env = os.Environ`,
		"diskcache#readfile":  `package diskcache; import "os"; var read = os.ReadFile`,
		"diskcache#writefile": `package diskcache; import "os"; var write = os.WriteFile`,
		"diskcache#go":        `package diskcache; func run(f func()) { go f() }`,
		"diskcache#log":       `package diskcache; import "log"; var out = log.Printf`,
		"diskcache#println":   `package diskcache; import "fmt"; var out = fmt.Println`,
		"diskcache#bufio":     `package diskcache; import "bufio"; var _ bufio.Writer`,
		"diskcache#syscall":   `package diskcache; import "syscall"; var _ = syscall.Flock`,
		"diskcache#unix":      `package diskcache; import "golang.org/x/sys/unix"; var _ = unix.Write`,
		"diskcache#exec":      `package diskcache; import "os/exec"; var _ = exec.Command`,
		"privatefs#stdout":    `package privatefs; import "os"; var out = os.Stdout`,
		"privatefs#remove":    `package privatefs; import "os"; var rm = os.Remove`,
		"privatefs#unixwrite": `package privatefs; import "golang.org/x/sys/unix"; var _ = unix.Write`,
		"privatefs#unixread":  `package privatefs; import u "golang.org/x/sys/unix"; var _ = u.Read`,
		"privatefs#go":        `package privatefs; func run(f func()) { go f() }`,
		"privatefs#exit":      `package privatefs; import "os"; var quit = os.Exit`,
		"safefile#stdout":     `package safefile; import "os"; var out = os.Stdout`,
		"safefile#remove":     `package safefile; import "os"; var rm = os.Remove`,
		"safefile#filepath":   `package safefile; import "path/filepath"; var _ = filepath.Join`,
		"safefile#flock":      `package safefile; import "syscall"; var _ = syscall.Flock`,
		// Round 2: the path package by name (Glob, Walk and EvalSymlinks
		// read the file system), and the raw system call with its one
		// number alone.
		"diskcache#glob":        `package diskcache; import "path/filepath"; var _ = filepath.Glob`,
		"diskcache#walk":        `package diskcache; import "path/filepath"; var _ = filepath.WalkDir`,
		"privatefs#evalsymlink": `package privatefs; import "path/filepath"; var _ = filepath.EvalSymlinks`,
		"privatefs#syscall#num": `package privatefs; import "golang.org/x/sys/unix"; func f() { unix.Syscall6(4, 0, 0, 0, 0, 0, 0) }`,
		"privatefs#syscall#var": `package privatefs; import "golang.org/x/sys/unix"; func f(n uintptr) { unix.Syscall6(n, 0, 0, 0, 0, 0, 0) }`,
		"privatefs#syscall#oth": `package privatefs; import "golang.org/x/sys/unix"; func f() { unix.Syscall6(unix.SYS_WRITE, 0, 0, 0, 0, 0, 0) }`,
		"privatefs#syscall#ali": `package privatefs; import u "golang.org/x/sys/unix"; func f() { u.Syscall6(u.SYS_WRITE, 0, 0, 0, 0, 0, 0) }`,
		// Review round 6: a number in a field of a variable passes the
		// package's names; the check of the first argument refuses it.
		"privatefs#syscall#fld": `package privatefs; import "golang.org/x/sys/unix"; var s struct{ n uintptr }; func f() { unix.Syscall6(s.n, 0, 0, 0, 0, 0, 0) }`,
	} {
		pkg, _, _ := strings.Cut(name, "#")
		if got := inspectUI(withFS(map[string]string{"internal/" + pkg + "/probe.go": source})); len(got) == 0 {
			t.Errorf("missed %s", name)
		}
	}
}

// The allowance is per package: the same names stay refused everywhere
// else the interface reaches, the root controller and the views included.
func TestTheFileSystemAllowanceReachesNoOtherPackage(t *testing.T) {
	for name, source := range map[string]string{
		"internal/ui/x.go":        `package ui; import "os"; var _ = os.Remove`,
		"internal/ui/view/x.go":   `package view; import "os"; var _ = os.Remove`,
		"internal/gateway/x.go":   `package gateway; import "os"; var _ = os.Remove`,
		"internal/cache/x.go":     `package cache; import "os"; var _ = os.Remove`,
		"internal/api/x.go":       `package api; import "path/filepath"; var _ = filepath.Join`,
		"internal/transport/x.go": `package transport; import "syscall"; var _ = syscall.O_NONBLOCK`,
		"internal/queue/x.go":     `package queue; import "golang.org/x/sys/unix"; var _ = unix.Syscall6`,
		"internal/worker/x.go":    `package worker; import "crypto/sha256"; var _ = sha256.Sum256`,
	} {
		files := withFS(map[string]string{name: source})
		files["internal/gateway/gateway.go"] = `package gateway; import ( _ "` + clientModule + `internal/api"; _ "` + clientModule + `internal/transport"; _ "` + clientModule + `internal/cache"; _ "` + clientModule + `internal/queue"; _ "` + clientModule + `internal/worker" )`
		files["internal/api/api.go"], files["internal/transport/transport.go"], files["internal/cache/cache.go"], files["internal/queue/queue.go"], files["internal/worker/worker.go"] = `package api`, `package transport`, `package cache`, `package queue`, `package worker`
		if got := inspectUI(files); len(got) == 0 {
			t.Errorf("missed %s", name)
		}
	}
	// A view cannot reach the three packages, directly or through a
	// helper.
	for name, source := range map[string]string{
		"direct cache":    `package view; import _ "` + clientModule + `internal/diskcache"`,
		"direct private":  `package view; import _ "` + clientModule + `internal/privatefs"`,
		"direct safefile": `package view; import _ "` + clientModule + `internal/safefile"`,
		"helper":          `package view; import _ "` + clientModule + `internal/helper"`,
	} {
		files := withFS(map[string]string{"internal/ui/view/pane.go": source, "internal/helper/helper.go": `package helper; import _ "` + clientModule + `internal/diskcache"`})
		if got := strings.Join(inspectUI(files), " "); !strings.Contains(got, "internal/ui/view/pane.go") {
			t.Errorf("a view reached the file system %s: %s", name, got)
		}
	}
}

// The real files pass, and a probe in each turns the guard red: the
// repository scan reads them.
func TestTheGuardReadsTheRealFileSystemPackages(t *testing.T) {
	root := repoRoot(t)
	sources := repositorySources(t)
	for _, name := range []string{"internal/diskcache/diskcache.go", "internal/privatefs/owner_unix.go", "internal/privatefs/acl_darwin.go", "internal/safefile/safefile.go", "internal/ui/disk.go"} {
		if _, ok := sources[name]; !ok {
			t.Fatalf("%s is not scanned", name)
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, probe := range []string{"\nfunc probe(f func()) { go f() }\n", "\nfunc probe() { print(1) }\n"} {
			files := map[string]string{}
			for k, v := range sources {
				files[k] = v
			}
			files[name] = string(data) + probe
			if got := inspectUI(files); len(got) == 0 {
				t.Errorf("%s with %q passed", name, probe)
			}
		}
	}
	if got := inspectUI(map[string]string{"internal/diskcache/x.go": `package diskcache; import "os"; var out = os.Stdout`, "internal/ui/x.go": reach}); len(got) == 0 {
		t.Error("an output stream in the disk cache passed")
	}
}
