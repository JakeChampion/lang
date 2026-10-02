package e2eselfhost

import (
	"os/exec"
	"testing"
)

// forKvCases cover `for (k, v) in m { … }` map destructuring iteration.
// The parser encodes the two names as "k,v"; the emitter iterates the
// map's parallel keys[]/values[] arrays by index, binding k = keys[i+1],
// v = values[i+1]. Exit codes cross-checked vs the Go backend.
var forKvCases = []struct {
	name string
	src  string
	exit int
}{
	{"sum-k-plus-v", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = Map { 1: 10, 2: 20, 3: 12 }; let total: i32 = 0; for (k, v) in m { total = total + k + v; } return total; }", 48},
	{"sum-values-built", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = map_new(4); m = m.insert(5, 100); m = m.insert(7, 50); let t: i32 = 0; for (k, v) in m { t = t + v; } return t; }", 150},
	{"count", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = Map { 1: 0, 2: 0, 3: 0, 4: 0 }; let c: i32 = 0; for (k, v) in m { c = c + 1; } return c + 38; }", 42},
	{"string-keys", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"a\", 40); m = m.insert(\"b\", 2); let t: i32 = 0; for (k, v) in m { t = t + v; } return t; }", 42},
}

// TestSelfHostForKvX86_64 — `for (k,v) in m` with the self-hosted
// x86-64 compiler.
func TestSelfHostForKvX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range forKvCases {
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

// TestSelfHostForKvArm64 — CI-gated arm64 counterpart.
func TestSelfHostForKvArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range forKvCases {
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
