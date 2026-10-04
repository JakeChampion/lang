package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostConstAggregateArm64PIE runs the const-aggregate table on BOTH
// arm64 ELF targets, which is the coverage whose absence let #7112 ship.
//
// `-target arm64-android` is a static PIE: ET_DYN, no PT_DYNAMIC, no
// relocations, mapped at a base the linker never knew. A const-aggregate block's
// shape word (#6149) and a static string box's data word (#7080) are ABSOLUTE
// pointers in `.data`, so both arrive stale by the load slide. The shape word
// going stale is silent — `match` compares it against an `adrp`-computed pointer
// that is correct, never matches, falls through every arm, and returns 0 rather
// than crashing. emit_ir_reloc's startup loop is what re-adds the slide.
//
// The x86-64 sibling (TestSelfHostConstAggregateIRX86_64) covers the same table
// through asm_run + gcc, and `internal/e2eselfhost/self_host_macho_test.go`
// covers darwin, where the LC_DYLD_INFO rebase stream hides the whole problem.
// Neither can see a PIE-only fault, and the existing android coverage
// (TestSelfHostCLIX86_64/emit-target-arm64-android) uses a program with no
// static constants at all.
//
// arm64-linux runs the same cases as the control: it is ET_EXEC, so the slide is
// zero and the loop is a no-op. A failure there is the loop corrupting data it
// should have left alone, which is the other way this can go wrong.
func TestSelfHostConstAggregateArm64PIE(t *testing.T) {
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not on PATH; skipping arm64 PIE const-aggregate e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	// The string-box cases are here rather than in the arm64-linux builds test
	// because they are the OTHER region the same loop walks, and they fail the
	// same silent way: a stale data word reads bytes from near address zero.
	// `.len()` is deliberately not the probe — it reads the length word and
	// passes with the data pointer wholly wrong.
	strBoxCases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{"literal_compare_derefs_the_box",
			`function main(): i32 { let s: string = "abc"; if (s == "abc") { return 7; } return 1; }`, 7},
		{"literal_index_derefs_the_box",
			`function main(): i32 { let s: string = "abcdef"; return (s[3] as i32) - 90; }`, 10},
		{"concat_reads_both_operands",
			`function main(): i32 { let s: string = "hello, " + "world!"; if (s == "hello, world!") { return 9; } return 1; }`, 9},
		// A capture-free function value is a static array box whose element is
		// the body's address; the relocation walk used to shift its length and
		// leave the address stale, so the call faulted under a slide.
		{"function_value_constant",
			`function dbl(x: i32): i32 { return x * 2; } function inc(x: i32): i32 { return x + 1; } @noinline function pick(k: i32): (i32) => i32 { if (k > 0) { return dbl; } return inc; } function main(): i32 { let f: (i32) => i32 = pick(1); let g: (i32) => i32 = pick(0); return f(20) + g(1); }`, 42},
		// A dyn value's vtable is a static table of function addresses, the
		// other constant whose every word is an absolute code pointer.
		{"dyn_trait_vtable",
			`trait Shape { function area(self: Self): i32; } struct Sq { s: i32 } impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } } function main(): i32 { let d: dyn Shape = Sq { s: 7 }; return d.area(); }`, 49},
		// All three static kinds in one pool: arrays of scalars, empty and not,
		// which hold no address and sit outside the walked regions, a record
		// constant and a function value, each in the region its layout is
		// walked by.
		{"every_static_kind",
			`struct P { a: i32, b: i32 } function mk(): P { return P { a: 5, b: 9 }; } function dbl(x: i32): i32 { return x * 2; } @noinline function pick(): (i32) => i32 { return dbl; } @noinline function ks(): i32[] { return [3, 1, 4]; } function main(): i32 { let ps: P[] = []; ps = ps.append(mk()); let f: (i32) => i32 = pick(); return f(ps[0].a) + ps[0].b + ps.len() + ks()[2]; }`, 24},
	}

	for _, target := range []string{"arm64-linux", "arm64-android"} {
		t.Run(target, func(t *testing.T) {
			run := func(t *testing.T, name, src string, wantExit int) {
				t.Helper()
				srcPath := filepath.Join(dir, name+"_"+target+".fern")
				if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				binPath := filepath.Join(dir, name+"_"+target+".bin")
				emit := exec.Command(fernBin, "-target", target, "-o", binPath, srcPath)
				emitOut, err := emit.CombinedOutput()
				if err != nil {
					t.Fatalf("emit -target %s: %v\n%s", target, err, emitOut)
				}
				if err := os.Chmod(binPath, 0o755); err != nil {
					t.Fatalf("chmod: %v", err)
				}
				cmd := exec.Command("qemu-aarch64", binPath)
				out, _ := cmd.CombinedOutput()
				if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
					t.Fatalf("%s did not exit normally (out=%q)", name, out)
				}
				if got := cmd.ProcessState.ExitCode(); got != wantExit {
					t.Errorf("%s exited %d, want %d (out=%q)", name, got, wantExit, out)
				}
			}

			for _, tc := range constAggCases {
				t.Run(tc.name, func(t *testing.T) { run(t, tc.name, tc.src, tc.expected) })
			}
			for _, tc := range strBoxCases {
				t.Run(tc.name, func(t *testing.T) { run(t, tc.name, tc.src, tc.wantExit) })
			}
		})
	}
}
