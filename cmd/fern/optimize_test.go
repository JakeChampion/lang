package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// -O elides assert() checks from compiled output: the same program exits
// 1 (the failing assert) on a default build and returns main's value on
// a release build. Uses the in-process x86-64 backend, so no external
// toolchain is needed — but executing the binary needs a linux/amd64
// host.
func TestOptimizeElidesAsserts(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("needs native linux/amd64 execution, host is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	prog := `function main(): i32 {
    assert(1 == 2, "always fails");
    return 42;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(optimize bool, out string) string {
		bin := filepath.Join(dir, out)
		code, err := run(src, bin, "x86-64-linux", "", "", "", false, false, "qemu-aarch64",
			false, false, false, nil, false, "", optimize, nil)
		if err != nil || code != 0 {
			t.Fatalf("build (optimize=%v): code=%d err=%v", optimize, code, err)
		}
		return bin
	}

	debugBin := build(false, "debug")
	cmd := exec.Command(debugBin)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("default build: failing assert should exit 1, got %d (output %q)", code, out)
	}
	if want := "assertion failed: always fails"; string(out) == "" || !contains(string(out), want) {
		t.Fatalf("default build stderr should carry %q, got %q", want, out)
	}

	relBin := build(true, "release")
	cmd = exec.Command(relBin)
	out, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("-O build: assert should be elided (exit 42), got %d (output %q)", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("-O build should print nothing, got %q", out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
