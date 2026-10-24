package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var knownGOOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "hurd": true, "illumos": true, "ios": true, "js": true,
	"linux": true, "nacl": true, "netbsd": true, "openbsd": true, "plan9": true,
	"solaris": true, "wasip1": true, "windows": true, "zos": true,
}

var knownGOARCH = map[string]bool{
	"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true,
	"mips": true, "mips64": true, "mips64le": true, "mipsle": true,
	"ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true,
	"sparc64": true, "wasm": true,
}

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

func hasPlatformConstraint(filename string) string {
	name := filename
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	i := strings.IndexByte(name, '_')
	if i < 0 {
		return ""
	}
	name = name[i:]
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
			if info.Name() == ".git" || info.Name() == "node_modules" || isNestedCheckout(root, path) {
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
