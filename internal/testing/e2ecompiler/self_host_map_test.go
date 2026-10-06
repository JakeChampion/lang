package e2ecompiler

import (
	"os/exec"
	"testing"
)

// mapCases exercise the self-host Map runtime (string-keyed, 8-byte
// values): map_new, set (insert + update), get → Option, has, len.
// Values cross-checked vs the Go backend.
var mapCases = []struct {
	name string
	src  string
	exit int
}{
	{"set-get-sum", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(8); m = m.insert(\"a\", 10); m = m.insert(\"b\", 32); let r: i32 = 0; match (m.get(\"a\")) { Some(v) => { r = r + v; }, None => { } } match (m.get(\"b\")) { Some(v) => { r = r + v; }, None => { } } return r; }", 42},
	{"update", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"a\", 10); m = m.insert(\"a\", 11); match (m.get(\"a\")) { Some(v) => { return v; }, None => { return 0; } } return 0; }", 11},
	{"has-len", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"x\", 7); m = m.insert(\"y\", 8); if (m.has(\"x\") && m.has(\"y\") && !m.has(\"z\")) { return m.len() + 40; } return 0; }", 42},
	{"get-absent", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"a\", 1); match (m.get(\"absent\")) { Some(v) => { return 1; }, None => { return 99; } } return 0; }", 99},
}

// TestSelfHostMapX86_64 compiles map programs with the self-hosted
// compiler and checks exit codes.
func TestSelfHostMapX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range mapCases {
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

// TestSelfHostMapArm64 — CI-gated arm64 counterpart.
func TestSelfHostMapArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range mapCases {
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
