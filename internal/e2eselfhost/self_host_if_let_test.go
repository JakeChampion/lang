package e2eselfhost

import (
	"os/exec"
	"testing"
)

// ifLetCases cover `if let PAT = EXPR { then } else { else }`, which the
// parser desugars to `match (EXPR) { PAT => { then }, _ => { else } }`.
// Covers the Some arm, the else arm (None), a no-else fall-through, and
// a user enum variant. Exit codes cross-checked vs the Go backend.
var ifLetCases = []struct {
	name string
	src  string
	exit int
}{
	{"some", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); if let Some(v) = m.get(\"k\") { return v; } else { return 1; } }", 42},
	{"none-else", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); if let Some(v) = m.get(\"absent\") { return v; } else { return 7; } }", 7},
	{"no-else-fallthrough", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 5); if let Some(v) = m.get(\"absent\") { return v; } return 9; }", 9},
	{"user-variant", "enum Shape { Circle(i32), Empty } function main(): i32 { let s: Shape = Circle(42); if let Circle(r) = s { return r; } else { return 0; } }", 42},
}

// TestSelfHostIfLetX86_64 — `if let` desugar with the self-hosted
// x86-64 compiler.
func TestSelfHostIfLetX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range ifLetCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostIfLetArm64 — CI-gated arm64 counterpart.
func TestSelfHostIfLetArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range ifLetCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
