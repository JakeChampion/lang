package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A driver's cache key has to move with everything that changes its bytes:
// the compiler that builds it and the stdlib it compiles in, which the
// self-host compiler reads from disk. Dropping either from the key would
// reuse a stale driver silently, with every test still passing on it.
func TestDriverCompilerKeyCoversTheCompilerAndTheStdlib(t *testing.T) {
	base := driverCompilerKeyFor("compiler-a", "stdlib-1")
	if driverCompilerKeyFor("compiler-a", "stdlib-1") != base {
		t.Fatal("the key is not deterministic")
	}
	if driverCompilerKeyFor("compiler-b", "stdlib-1") == base {
		t.Error("a different compiler produced the same key")
	}
	if driverCompilerKeyFor("compiler-a", "stdlib-2") == base {
		t.Error("a different stdlib produced the same key")
	}
}

func TestTreeHashSeesEveryKindOfEdit(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hash := func() string {
		t.Helper()
		h, err := treeHash(root)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	write("core/int.fern", "a")
	write("std/test.fern", "b")
	h0 := hash()
	if hash() != h0 {
		t.Fatal("the hash is not deterministic")
	}
	write("core/int.fern", "a2")
	h1 := hash()
	if h1 == h0 {
		t.Error("editing a file did not change the hash")
	}
	write("core/new.fern", "c")
	h2 := hash()
	if h2 == h1 {
		t.Error("adding a file did not change the hash")
	}
	if err := os.Rename(filepath.Join(root, "core/new.fern"), filepath.Join(root, "core/renamed.fern")); err != nil {
		t.Fatal(err)
	}
	if hash() == h2 {
		t.Error("renaming a file did not change the hash")
	}
	if err := os.Remove(filepath.Join(root, "core/renamed.fern")); err != nil {
		t.Fatal(err)
	}
	if hash() != h1 {
		t.Error("removing the added file did not restore the hash")
	}
}

func TestStdlibHashCoversTheStdlibTree(t *testing.T) {
	root := SelfHostStdlibRoot(t)
	want, err := treeHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := stdlibHash(t); got != want {
		t.Errorf("stdlibHash = %s, treeHash(%s) = %s", got, root, want)
	}
	if _, err := os.Stat(filepath.Join(root, "core", "int.fern")); err != nil {
		t.Errorf("the stdlib root does not hold core/int.fern: %v", err)
	}
}

func TestDriverBuildWeightIsPerDriver(t *testing.T) {
	if w := DriverBuildWeightMB("fern.fern"); w <= DriverBuildWeightMB("asm_ir_run.fern") {
		t.Errorf("fern.fern reserves %d MB, no more than a smaller driver", w)
	}
	if w := DriverBuildWeightMB("asm_ir_run.fern"); w <= 0 {
		t.Errorf("driver weight = %d; want positive", w)
	}
}

// `bootstrap.sh stage0` is what Stage0Compiler parses: the resolved path on
// stdout and nothing else, progress on stderr. STAGE0=<path> is the
// override the test uses so no download runs.
func TestBootstrapStage0PrintsThePathAlone(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "fern-selfhost")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join(repoRootDir, "bootstrap", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script, "stage0")
	cmd.Env = append(os.Environ(), "STAGE0="+fake)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bootstrap.sh stage0: %v\n%s", err, stderr.String())
	}
	if got := string(out); got != fake+"\n" {
		t.Errorf("stdout = %q, want the path %q alone", got, fake+"\n")
	}
	if !strings.Contains(stderr.String(), "stage0: "+fake) {
		t.Errorf("stderr does not report the resolved stage0:\n%s", stderr.String())
	}
}
