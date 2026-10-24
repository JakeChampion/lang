// CI-DARK: FERN_X86_64_VERDICT_CHILD — The test uses this flag to identify its child process.
package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestX86_64ToolingMissingVerdict(t *testing.T) {
	if os.Getenv("FERN_X86_64_VERDICT_CHILD") == "1" {
		X86_64Tooling(t)
		return
	}
	for _, tc := range []struct {
		name     string
		require  string
		wantFail bool
		wantText string
	}{
		{name: "skips by default", require: "", wantFail: false, wantText: "on PATH"},
		{name: "fails when required", require: "1", wantFail: true, wantText: "covers nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run", "^TestX86_64ToolingMissingVerdict$", "-test.v")
			cmd.Env = append(os.Environ(),
				"FERN_X86_64_VERDICT_CHILD=1",
				"PATH="+t.TempDir(),
				"FERN_REQUIRE_X86_64_TOOLING="+tc.require,
			)
			out, err := cmd.CombinedOutput()
			if gotFail := err != nil; gotFail != tc.wantFail {
				t.Fatalf("child failed=%v, want %v\n%s", gotFail, tc.wantFail, out)
			}
			if !strings.Contains(string(out), tc.wantText) {
				t.Fatalf("child output missing %q:\n%s", tc.wantText, out)
			}
		})
	}
}

func TestLookupX86_64RunnerFollowsTheHost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	runner, ok := lookupX86_64Runner()
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		if !ok || runner != nil {
			t.Fatalf("x86-64 host: runner=%v ok=%v, want native (nil, true)", runner, ok)
		}
		return
	}
	if ok {
		t.Fatalf("empty PATH on a non-x86-64 host: runner=%v ok=true, want no runner", runner)
	}
	fake := filepath.Join(dir, "qemu-x86_64")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner, ok = lookupX86_64Runner()
	if !ok || len(runner) != 1 || runner[0] != fake {
		t.Fatalf("qemu-x86_64 on PATH: runner=%v ok=%v, want [%s]", runner, ok, fake)
	}
}
