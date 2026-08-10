// Package sourcelint holds fast, dependency-free repo-hygiene checks that run
// in the ordinary `go test ./...` lane (no build tools, no fixtures).
package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// knownGOOS / knownGOARCH are the tokens the go tool treats as an implicit
// build constraint when they are the LAST underscore-separated segment of a
// source file's name (after stripping a trailing `_test`). A test file named
// `foo_wasm_test.go` or `foo_arm64_test.go` therefore compiles ONLY under that
// GOOS/GOARCH — and since this project's test binaries always run natively on
// the host (arm64/wasm targets are exercised by compiling PROGRAMS and running
// them under qemu/wasmtime, never by cross-building the Go test binary), such a
// file is silently excluded from every CI lane and every local run: it never
// compiles in, so it can never fail. #5464 (an arm64 SSA-emit suite that had
// never run; and two earlier `_wasm_test.go` files) is exactly this trap.
//
// The project convention is to place the target token in the MIDDLE of the name
// (`self_host_ssa_wasm_emit_test.go`, `..._arm64_ir_test.go`) so the last
// segment is a non-token word. This guard enforces that: a test file whose name
// ends in a GOOS/GOARCH token before `_test.go` fails the build here rather than
// disappearing. Restricted to `_test.go` files — a legitimately
// platform-specific non-test file (`foo_arm64.go`) is a real, intended build
// constraint and is left alone.
var knownGOOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "hurd": true, "illumos": true, "ios": true, "js": true,
	"linux": true, "nacl": true, "netbsd": true, "openbsd": true, "plan9": true,
	"solaris": true, "wasip1": true, "windows": true, "zos": true,
}

var knownGOARCH = map[string]bool{
	"386": true, "amd64": true, "arm": true, "arm64-linux": true, "loong64": true,
	"mips": true, "mips64": true, "mips64le": true, "mipsle": true,
	"ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true,
	"sparc64": true, "wasm32-wasi": true,
}

// moduleRoot walks up from the test's working directory to the directory
// holding go.mod (the module root), so the guard scans the whole tree
// regardless of which package directory `go test` runs it from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from working directory")
		}
		dir = parent
	}
}

// hasPlatformConstraint mirrors go/build's goodOSArchFile filename rule: strip
// the extension, then IGNORE everything up to and including the first
// underscore (so a bare `arm64.go` / `arm64_test.go` is NOT auto-tagged — the
// `arm64` sits before the first `_` and is cut), and check the final one or two
// segments of the remainder against the known GOOS/GOARCH token sets (a trailing
// `_test` is dropped first). Returns the constraining token, or "" for none.
func hasPlatformConstraint(filename string) string {
	name := filename
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	i := strings.IndexByte(name, '_')
	if i < 0 {
		return "" // no underscore → the leading word is cut, nothing to tag
	}
	name = name[i:] // keep from the first '_' (inclusive), e.g. "_ssa_emit_arm64_test"
	l := strings.Split(name, "_")
	if n := len(l); n > 0 && l[n-1] == "test" {
		l = l[:n-1]
	}
	n := len(l)
	if n >= 2 && knownGOOS[l[n-2]] && knownGOARCH[l[n-1]] {
		return l[n-2] + "_" + l[n-1]
	}
	if n >= 1 {
		if knownGOOS[l[n-1]] || knownGOARCH[l[n-1]] {
			return l[n-1]
		}
	}
	return ""
}

func TestNoPlatformSuffixOnTestFiles(t *testing.T) {
	root := moduleRoot(t)
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if tok := hasPlatformConstraint(name); tok != "" {
			rel, _ := filepath.Rel(root, path)
			offenders = append(offenders, rel+"  (token: "+tok+")")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("these test files end in a GOOS/GOARCH token before `_test.go`, so the go tool "+
			"applies an implicit build constraint and silently excludes them from native test runs "+
			"(they never compile in, so they never fail — see #5464). Move the target token out of the "+
			"final name segment (e.g. `_ssa_arm64_emit_test.go`, `_wasm_ir_test.go`):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
