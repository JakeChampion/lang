package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The target-OS compile-time constant (#8338).
//
// conformance/cases/target_os_constant pins the CONTRACT on every backend —
// the OS is named and exactly one family predicate agrees with it — because a
// conformance case cannot say a different value per backend. This file pins
// the VALUES, which is only expressible where the target is named.

const targetOSProg = `import "std/platform";
function main(): i32 { print(platform.OS); return 0; }`

func TestTargetOSValueX86_64(t *testing.T) {
	out, code := compileAndRunX86_64(t, targetOSProg)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if strings.TrimSpace(out) != "linux" {
		t.Errorf("x86-64-linux says %q, want linux", strings.TrimSpace(out))
	}
}

func TestTargetOSValueArm64(t *testing.T) {
	out, code := compileAndRunArm64(t, targetOSProg)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if strings.TrimSpace(out) != "linux" {
		t.Errorf("arm64-linux says %q, want linux", strings.TrimSpace(out))
	}
}

func TestTargetOSValueWasm(t *testing.T) {
	out := runWasmCapturingStdout(t, targetOSProg)
	if strings.TrimSpace(out) != "wasi" {
		t.Errorf("wasm32-wasi says %q, want wasi", strings.TrimSpace(out))
	}
}

// foldProg guards each branch with a string only that branch can reach, so
// "which branch survived" is answerable by reading the emitted binary — which
// is what lets the darwin leg run on a Linux host.
const foldProg = `import "std/platform";
function main(): i32 {
    if (platform.IS_DARWIN) { print("MARKER_DARWIN_BRANCH"); return 1; }
    print("MARKER_LINUX_BRANCH");
    return 0;
}`

// The dead branch is GONE from the binary, string literal and all: the
// comparison folded to a bool literal before the checker ran. This is the
// property the issue asks for — a per-target choice that costs nothing at run
// time — and a plain value test cannot see it.
func TestTargetOSDeadBranchIsNotEmitted(t *testing.T) {
	for _, tc := range []struct {
		target, keep, drop string
	}{
		{"x86-64-linux", "MARKER_LINUX_BRANCH", "MARKER_DARWIN_BRANCH"},
		{"arm64-linux", "MARKER_LINUX_BRANCH", "MARKER_DARWIN_BRANCH"},
		// Built on whatever host runs this: the value is the TARGET's, never
		// the compiler's machine (docs/COMPTIME-BRIEF.md rule 1).
		{"arm64-darwin", "MARKER_DARWIN_BRANCH", "MARKER_LINUX_BRANCH"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			raw := buildForTarget(t, tc.target, foldProg)
			if !strings.Contains(string(raw), tc.keep) {
				t.Errorf("%s: the live branch's marker %q is missing from the binary", tc.target, tc.keep)
			}
			if strings.Contains(string(raw), tc.drop) {
				t.Errorf("%s: the dead branch's marker %q survived — the comparison did not fold, so the branch reached codegen",
					tc.target, tc.drop)
			}
		})
	}
}

// buildForTarget compiles src for `target` and returns the emitted bytes. No
// runner is involved, so a target this host cannot execute is still checkable.
func buildForTarget(t *testing.T, target, src string) []byte {
	t.Helper()
	bin := e2eharness.BuildLangBinForInterp(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", target, "-o", out, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("%s build failed: %v\n%s", target, err, o)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s output: %v", target, err)
	}
	return raw
}
