package e2eselfhost

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64LinuxBuilds is the arm64-Linux flagship: the self-host CLI
// (examples/self_host/fern.fern, `-target arm64`) now emits a runnable static
// ELF **in-process** — asm_arm64 / ssa_arm64 produce the GAS text and
// arm64_native + elf.fern assemble + link it, with no `.s` + gcc/ld step (the
// flip, mirroring arm64-darwin). Unlike the darwin path (which can only be
// executed on a macOS runner), the arm64-Linux output runs on the Linux CI
// box under qemu-aarch64 — so this EXECUTES each program and checks its exit
// code, the decisive end-to-end check of the flipped path.
//
// The CLI is built with the Go x86-64 backend so it runs on the host; it then
// emits arm64 ELF binaries straight to `-o`, which qemu-aarch64 runs.
func TestSelfHostArm64LinuxBuilds(t *testing.T) {
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not on PATH; skipping arm64-linux self-host e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	// wantOut is checked when non-empty. It exists because the `print` case
	// below compared ONLY the exit code for as long as it existed, so it could
	// not fail for the reason it was written: #6047 had every printed string
	// coming out the right length with the wrong bytes ("Hello, Fern!" ->
	// "eeeeeeeeeeeeF") on this exact path, and this test stayed green through it.
	// A print case that ignores stdout is not a print case.
	cases := []struct {
		name     string
		src      string
		wantExit int
		wantOut  string
	}{
		{"exit_42", `function main(): i32 { return 42; }`, 42, ""},
		{"arith", `function main(): i32 { var x = 6; var y = 7; return x * y; }`, 42, ""},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`, 55, ""},
		{"loop_sum", `function main(): i32 { var s: i32 = 0; var i: i32 = 1; while (i <= 10) { s = s + i; i = i + 1; } return s; }`, 55, ""},
		{"concat", `function main(): i32 { var s: string = "hello, " + "world!"; return s.len(); }`, 13, ""},
		{"print", `function main(): i32 { print("hi"); return 0; }`, 0, "hi\n"},
		// The #6047 shapes: a literal long enough that a stuck source index
		// repeats a byte visibly, and a runtime-built string (concat, which is
		// where the base+index `ldrb w0, [x0, x1]` copy loop lives).
		{"print_literal", `function main(): i32 { print("Hello, Fern!"); return 0; }`, 0, "Hello, Fern!\n"},
		{"print_concat", `function main(): i32 { var s: string = "Hello, " + "Fern!"; print(s); return 0; }`, 0, "Hello, Fern!\n"},
		{"index_byte", `function main(): i32 { var s: string = "abcdef"; return (s[3] as i32) - 90; }`, 10, ""},
		{"struct_method", `struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { var x = Box { v: 4 }; return x.scale(3); }`, 12, ""},
		{"array_sum", `function main(): i32 { var a = [1, 2, 3, 4, 5]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 15, ""},
		{"option", `function pick(n: i32): Option[i32] { if (n == 0) { return None; } return Some(n + 1); } function main(): i32 { match (pick(41)) { Some(v) => { return v; }, None => { return 0; } } return 99; }`, 42, ""},
		{"enum", `enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r*r*3; }, Square(w) => { return w*w; } } } function main(): i32 { return area(Circle(2)) + area(Square(3)); }`, 21, ""},
		{"floats", `function main(): i32 { var x: f64 = 3.5; var y: f64 = 2.0; var z: f64 = x*y + x/y - x; if (z > 5.0) { return 7; } return 1; }`, 7, ""},
		// The bit-counting intrinsics: the only shapes that make the arm64
		// backend emit `rbit` (ctz) or the SIMD pair `cnt`/`addv` (popcount),
		// and hence the only ones that exercise those encoders in
		// arm64_native. Zero comes first — it is the input the op definition
		// pins (clz/ctz of 0 = the operand width).
		{"bitcount", `function main(): i32 {
    if (__ctz32(0 as u32) != 32) { return 1; }
    if (__ctz64(0 as u64) != 64) { return 2; }
    if (__popcount32(0 as u32) != 0) { return 3; }
    if (__popcount64(0 as u64) != 0) { return 4; }
    if (__ctz32(16 as u32) != 4) { return 5; }
    if (__ctz64(1048576 as u64) != 20) { return 6; }
    if (__popcount32(4294967295 as u32) != 32) { return 7; }
    if (__popcount64(1023 as u64) != 10) { return 8; }
    if (__clz32(1 as u32) != 31) { return 9; }
    if (__clz64(1 as u64) != 63) { return 10; }
    return 42;
}`, 42, ""},
		// The f64 transcendentals through the SELF-HOST emitter. Every check
		// here fails on the kernels this replaced: the domain cases returned
		// finite garbage (exp(1000) was -6.1e-183 because 2^k is assembled as
		// (k+1023)<<52 and k=1443 overflows into the sign bit; log(0) was
		// -709.09; log(-1) was 0), and sin(10) was off in the 7th digit
		// because pi/2 was carried as two chunks rather than three.
		{"transcendental", `function main(): i32 {
    if (!(__exp_f64(1000.0) > 1.0e308)) { return 1; }        // must overflow to +Inf
    if (!(__exp_f64((0.0 - 1000.0)) == 0.0)) { return 2; }   // must underflow to 0
    if (!(__log_f64(0.0) < (0.0 - 1.0e308))) { return 3; }   // must be -Inf
    var n: f64 = __log_f64((0.0 - 1.0));                     // must be NaN
    if (n == n) { return 4; }
    if ((__pow_f64(3.0, 2.0) as i32) != 9) { return 5; }     // exact, not 8
    if ((__pow_f64(10.0, 3.0) as i32) != 1000) { return 6; }
    var d: f64 = __sin_f64(10.0) - (0.0 - 0.54402111088936981);
    if (d < 0.0) { d = 0.0 - d; }
    if (d > 1.0e-15) { return 7; }                           // 3-part reduction
    return 42;
}`, 42, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			binPath := filepath.Join(dir, c.name+".bin")
			if out, err := exec.Command(fernBin, "-target", "arm64", "-o", binPath, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("self-host emit (arm64 ELF) failed: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("read bin: %v", err)
			}
			f, err := elf.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("output is not a parseable ELF: %v", err)
			}
			if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_EXEC {
				t.Fatalf("got machine=%v type=%v, want AARCH64/EXEC", f.Machine, f.Type)
			}
			// The compiler must emit a RUNNABLE binary. This used to be an
			// os.Chmod: the self-host CLI wrote 0644 where the native one
			// writes 0755, and the harness quietly repaired it, so nothing
			// gated the mode. That repair is why a hand-run of the same
			// command exited 1 with no output — indistinguishable from a
			// program that ran and returned 1, and it cost one investigation
			// a fabricated reproduction before anyone checked the mode
			// (#6133). Asserting it here is what keeps the fix honest.
			fi, err := os.Stat(binPath)
			if err != nil {
				t.Fatalf("stat output: %v", err)
			}
			if fi.Mode().Perm()&0o111 == 0 {
				t.Fatalf("self-host wrote a NON-EXECUTABLE binary (mode %04o); "+
					"the CLI must write_file_exec for a target that emits one", fi.Mode().Perm())
			}
			cmd := exec.Command("qemu-aarch64", binPath)
			var so, se bytes.Buffer
			cmd.Stdout, cmd.Stderr = &so, &se
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != c.wantExit {
				t.Errorf("self-host arm64 %q exit = %d, want %d (stderr: %s)", c.name, code, c.wantExit, se.String())
			}
			// Byte-for-byte: #6047 produced output of exactly the right LENGTH,
			// so a length or prefix check would have passed through it.
			if c.wantOut != "" && so.String() != c.wantOut {
				t.Errorf("self-host arm64 %q stdout = %q, want %q", c.name, so.String(), c.wantOut)
			}
		})
	}
}
