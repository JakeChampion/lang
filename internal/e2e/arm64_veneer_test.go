package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestArm64VeneerForcedReach compiles and runs ordinary programs with the
// assembler's branch span shortened to a few dozen instructions
// (FERN_ARM64_VENEER_REACH), so every call in them goes through a
// trampoline instead of encoding directly.
//
// Without it the veneer path is reachable only by a ~130 MB program,
// which means one shape of generated code gets veneered and nothing
// else — and it is the whole emitted corpus, not one synthetic program,
// that has to survive a trampoline between caller and callee (a veneer
// clobbers x17, and islands are spliced into the middle of the code).
//
// This is not a hypothetical: forcing the span here is what found the
// miscompile where a second veneering pass spliced an island inside an
// island whose hop-over `b` was a hand-encoded offset the index remap
// could not correct. `float_to_string` below is the program that hung.
func TestArm64VeneerForcedReach(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()

	cases := []struct {
		name     string
		src      string
		wantOut  string
		wantExit int
	}{
		{"exit_code", `function main(): i32 { return 42; }`, "", 42},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n-1) + fib(n-2); } function main(): i32 { return fib(12); }`, "", 144},
		{"print", `function main(): i32 { print("hello"); return 0; }`, "hello\n", 0},
		{"strings", `function main(): i32 { var s = "a" + "bc"; print(s); return s.len(); }`, "abc\n", 3},
		// The float formatter is a long runtime with its own loops and
		// literal pools — the program that hung on the nested-island bug.
		{"float_to_string", `import "std/float"; function main(): i32 { print((0.0 - 2.25).to_string()); return 0; }`, "-2.25\n", 0},
		{"array_sum", `function main(): i32 { var a = [1,2,3,4,5]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, "", 15},
		{"struct_enum", `enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r*r*3; }, Square(w) => { return w*w; } } } function main(): i32 { return area(Circle(2)) + area(Square(3)); }`, "", 21},
		{"closure", `function main(): i32 { var n = 7; var f = (x: i32) => x + n; return f(35); }`, "", 42},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src+"\n"), 0o644); err != nil {
				t.Fatalf("write source: %v", err)
			}
			out := filepath.Join(dir, c.name+".bin")
			build := exec.Command(bin, "-target", "arm64-linux", "-o", out, src)
			// 64 instructions: short enough that every cross-function
			// call needs a veneer, long enough that a function's own
			// conditional branches (which are not veneered) still encode.
			build.Env = append(os.Environ(), "FERN_ARM64_VENEER_REACH=64")
			if o, err := build.CombinedOutput(); err != nil {
				t.Fatalf("arm64 build under a forced branch span failed: %v\n%s", err, o)
			}

			cmd := exec.Command(out)
			if qemu != "" {
				cmd = exec.Command(qemu, out)
			}
			got, _ := cmd.Output()
			if string(got) != c.wantOut {
				t.Errorf("stdout = %q, want %q", got, c.wantOut)
			}
			if code := cmd.ProcessState.ExitCode(); code != c.wantExit {
				t.Errorf("exit = %d, want %d", code, c.wantExit)
			}
		})
	}
}
