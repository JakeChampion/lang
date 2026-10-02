package e2eselfhost

import (
	"os/exec"
	"testing"
)

// mapKeysValuesCases cover `m.keys()` / `m.values()` — the map box
// already holds the parallel keys[]/values[] arrays at offset 0/8, so
// these return them directly. The result is an array of the key/value
// type (array_i32 for i32 keys/values, array_string for strings), so
// it iterates and `.len()` chains off it. Exit codes cross-checked vs
// the Go backend.
var mapKeysValuesCases = []struct {
	name string
	src  string
	exit int
}{
	{"keys-sum-literal", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = Map { 10: 1, 20: 2, 12: 3 }; let t: i32 = 0; for x in m.keys() { t = t + x; } return t; }", 42},
	{"keys-sum-built", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = map_new(4); m = m.insert(7, 0); m = m.insert(35, 0); let t: i32 = 0; for x in m.keys() { t = t + x; } return t; }", 42},
	{"values-sum", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = map_new(4); m = m.insert(1, 10); m = m.insert(2, 20); let t: i32 = 0; for x in m.values() { t = t + x; } return t; }", 30},
	{"keys-len-string", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"ab\", 1); m = m.insert(\"c\", 2); return m.keys().len() + 40; }", 42},
}

// TestSelfHostMapKeysX86_64 — m.keys()/m.values() with the self-hosted
// x86-64 compiler.
func TestSelfHostMapKeysX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range mapKeysValuesCases {
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

// TestSelfHostMapKeysArm64 — CI-gated arm64 counterpart.
func TestSelfHostMapKeysArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range mapKeysValuesCases {
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
