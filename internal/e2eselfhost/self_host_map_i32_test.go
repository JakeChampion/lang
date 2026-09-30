package e2eselfhost

import (
	"os/exec"
	"testing"
)

// mapI32Cases exercise i32-keyed maps (Map[i32, V]). The Map runtime
// compares keys with __fern_str_eq for string keys; for i32 keys the
// dispatch passes a key-kind flag and the runtime takes an integer
// (`==`) compare path instead. Covers set/update/has/len and the
// Option-returning get. Exit codes cross-checked vs the Go backend.
var mapI32Cases = []struct {
	name string
	src  string
	exit int
}{
	{"set-get-update", "import \"core/map\"; function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 40); m = m.insert(11, 99); m = m.insert(7, 42); if (m.len() != 2) { return 1; } if (!m.has(11)) { return 2; } match (m.get(7)) { Some(v) => { return v; }, None => { return 3; } } }", 42},
	{"absent-get", "import \"core/map\"; function main(): i32 { var m: Map[i32,i32] = map_new(4); m = m.insert(100, 5); m = m.insert(200, 7); match (m.get(999)) { Some(v) => { return v; }, None => { return 42; } } }", 42},
	{"has-absent", "import \"core/map\"; function main(): i32 { var m: Map[i32,i32] = map_new(4); m = m.insert(1, 1); if (m.has(1) && !m.has(2)) { return 7; } return 0; }", 7},
	{"i32-to-string-val", "import \"core/map\"; function main(): i32 { var m: Map[i32, string] = map_new(4); m = m.insert(1, \"hello\"); match (m.get(1)) { Some(s) => { return s.len(); }, None => { return 0; } } }", 5},
}

// TestSelfHostMapI32X86_64 — i32-keyed maps with the self-hosted
// x86-64 compiler.
func TestSelfHostMapI32X86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range mapI32Cases {
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

// TestSelfHostMapI32Arm64 — CI-gated arm64 counterpart.
func TestSelfHostMapI32Arm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range mapI32Cases {
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
