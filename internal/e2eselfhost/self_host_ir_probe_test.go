package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIREligibilityProbe exercises the asm_ir_run driver's `-ir-probe`
// flag, which prints asm_ir.eligibility_report(mod) instead of emitting asm: a
// per-function breakdown of what lowers through the stack-IR path vs what bails
// out, plus the module verdict the `-ir` path routes on.
//
// This makes the IR-subset frontier OBSERVABLE — the prerequisite for goal-1
// widening work, which otherwise can't tell whether a construct moved onto the
// IR path (the differential gate only proves "still correct", not "now lowers").
// The cases pin the four signals the report distinguishes: a fully-eligible
// module, a per-function `BAIL call` (a call to an unknown / builtin-only name),
// the no-main "module: AST" verdict, and — as a regression guard on a frontier
// comment that had gone stale — that break/continue inside a `for x in arr`
// body DOES lower (the increment-at-top loop shape, #2788, makes `continue`
// safe, so these statements are on the IR path, not a bail).
func TestSelfHostIREligibilityProbe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	// The probe driver = asm_ir_run.fern (writeSelfHostAsmProject copies its
	// ./-imports; std/io resolves from the real stdlib root).
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun")

	probe := func(t *testing.T, prog string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir-probe")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir-probe")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe driver failed for %q: %v", prog, err)
		}
		return string(out)
	}

	cases := []struct {
		name        string
		src         string
		wantVerdict string   // a line the report must contain
		wantLines   []string // per-function lines the report must contain
	}{
		{
			name:        "pure-i32",
			src:         "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(2, 3); }",
			wantVerdict: "module: IR",
			wantLines:   []string{"add: ir", "main: ir"},
		},
		{
			name:        "unknown-call-bails",
			src:         "function main(): i32 { return mystery(3); }",
			wantVerdict: "module: AST",
			wantLines:   []string{"main: BAIL call"},
		},
		{
			// A MAIN-LESS module routes IR (#3457 slice 5). A whole-program
			// `_start` that emits `call __fn_main` unconditionally forces a
			// require_main gate flag — and this
			// case pinned that refusal. `_start` exits 0 when there is no main, the
			// flag is deleted, and the shape is covered end-to-end (link + run) by
			// TestSelfHostNoMainModuleIRX86_64. Note `helper: ir` was ALREADY the
			// expectation here: every function lowered even then, which is what made
			// the old verdict a module-level gate rather than a lowering gap.
			name:        "no-main-is-ir",
			src:         "function helper(): i32 { return 1; }",
			wantVerdict: "module: IR",
			wantLines:   []string{"helper: ir"},
		},
		{
			// Regression guard: break/continue in a for-x-in-array body lowers.
			name:        "break-continue-array-for-lowers",
			src:         "function main(): i32 {\n var acc: i32 = 0;\n var xs: i32[] = [1, 2, 3, 4, 5];\n for x in xs { if (x == 2) { continue; } if (x == 4) { break; } acc = acc + x; }\n return acc;\n}",
			wantVerdict: "module: IR",
			wantLines:   []string{"main: ir"},
		},
		{
			// Method receivers lower; the report keys them by the dispatch label.
			name:        "method-receiver-lowers",
			src:         "struct P { x: i32 }\nfunction (p: P) get(): i32 { return p.x; }\nfunction main(): i32 { var p = P { x: 7 }; return p.get(); }",
			wantVerdict: "module: IR",
			wantLines:   []string{"P.get: ir", "main: ir"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := probe(t, c.src)
			if !strings.Contains(rep, c.wantVerdict) {
				t.Errorf("verdict: report missing %q\n--- report ---\n%s", c.wantVerdict, rep)
			}
			for _, line := range c.wantLines {
				if !strings.Contains(rep, line) {
					t.Errorf("report missing line %q\n--- report ---\n%s", line, rep)
				}
			}
		})
	}
}

// TestSelfHostIRPipelineProbe exercises the asm_load_run driver's `-ir-probe`
// flag — the pipeline-level companion to TestSelfHostIREligibilityProbe. Where
// the asm_ir_run probe sees a single parsed module, this loader-driven probe
// reports eligibility for the WHOLE program after its `import "std/…"` modules
// are resolved from disk and flattened in, so it reveals the IR-vs-AST frontier
// that real (stdlib-using) programs actually hit — the worklist for goal-1
// widening. The cases pin: a self-contained module reports per-function + a
// verdict, and a stdlib-importing program's report includes the mangled
// stdlib functions (proving the whole loaded program is probed, not just the
// entry module).
func TestSelfHostIRPipelineProbe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	// asm_load_run pulls in flatten + checker on top of the core emitter set.
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// probe writes prog to a temp entry file and runs `<driver> <entry> [root]
	// -ir-probe`, returning the report. root="" omits the stdlib root.
	probe := func(t *testing.T, prog, root string) string {
		t.Helper()
		entry := filepath.Join(dir, "probe_entry.fern")
		if err := os.WriteFile(entry, []byte(prog), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		args := []string{entry}
		if root != "" {
			args = append(args, root)
			// With a stdlib root the loader treeshakes by default (added later),
			// which would prune the imported module's functions this probe asserts
			// on (it verifies the loader pulls the whole flattened program into the
			// report, not just the entry module). Opt out so they remain visible.
			args = append(args, "-no-treeshake")
		}
		args = append(args, "-ir-probe")
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe driver failed for %q: %v", prog, err)
		}
		return string(out)
	}

	t.Run("self-contained", func(t *testing.T) {
		rep := probe(t, "function helper(n: i32): i32 { return n * 2; }\nfunction main(): i32 { return helper(21); }", "")
		for _, want := range []string{"helper: ir", "main: ir", "module: IR"} {
			if !strings.Contains(rep, want) {
				t.Errorf("report missing %q\n--- report ---\n%s", want, rep)
			}
		}
	})

	// probeArgs runs the driver with an arbitrary trailing arg list (the entry
	// path is prepended), returning its stdout — for exercising the sharded
	// `-ir-probe-range` flag.
	probeArgs := func(t *testing.T, prog string, extra ...string) string {
		t.Helper()
		entry := filepath.Join(dir, "probe_range_entry.fern")
		if err := os.WriteFile(entry, []byte(prog), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		args := append([]string{entry}, extra...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe driver failed (args %v): %v", extra, err)
		}
		return string(out)
	}

	t.Run("sharded-probe-range", func(t *testing.T) {
		// Five functions in a stable source order. The whole-module `-ir-probe`
		// and the sharded `-ir-probe-range` must agree on each function's verdict;
		// sharding is the heap-bounded way to audit the large self-host modules
		// (the whole-module probe OOMs lowering every function at once).
		prog := "function a(): i32 { return 1; }\n" +
			"function b(): i32 { return 2; }\n" +
			"function c(): i32 { return 3; }\n" +
			"function d(): i32 { return 4; }\n" +
			"function main(): i32 { return a() + b() + c() + d(); }"
		full := probe(t, prog, "")

		// The first shard (start 0) prints a "funcs: <N>" header for driving the
		// loop; N must be at least the 5 functions defined here.
		head := probeArgs(t, prog, "-ir-probe-range", "0", "2")
		if !strings.Contains(head, "funcs: ") {
			t.Errorf("range start 0 missing 'funcs:' header\n--- out ---\n%s", head)
		}
		// First two functions appear, the third does not (count = 2).
		if !strings.Contains(head, "a: ir") || !strings.Contains(head, "b: ir") {
			t.Errorf("range [0,2) missing a/b\n--- out ---\n%s", head)
		}
		if strings.Contains(head, "c: ir") {
			t.Errorf("range [0,2) leaked c beyond the count\n--- out ---\n%s", head)
		}

		// A non-zero start omits the header and reports only its slice.
		tail := probeArgs(t, prog, "-ir-probe-range", "2", "2")
		if strings.Contains(tail, "funcs: ") {
			t.Errorf("non-zero start should not print the 'funcs:' header\n--- out ---\n%s", tail)
		}
		if !strings.Contains(tail, "c: ir") || !strings.Contains(tail, "d: ir") {
			t.Errorf("range [2,4) missing c/d\n--- out ---\n%s", tail)
		}
		if strings.Contains(tail, "a: ir") || strings.Contains(tail, "main: ir") {
			t.Errorf("range [2,4) leaked an out-of-slice function\n--- out ---\n%s", tail)
		}

		// Every per-function verdict the sharded run reports must match the
		// whole-module probe's verdict for that function — sharding changes only
		// WHICH functions are reported, never the verdict.
		for _, fn := range []string{"a", "b", "c", "d", "main"} {
			want := fn + ": ir"
			if !strings.Contains(full, want) {
				t.Fatalf("whole-module probe missing %q\n--- out ---\n%s", want, full)
			}
		}
	})

	t.Run("stdlib-loaded-whole-program", func(t *testing.T) {
		// Importing a stdlib module pulls its (mangled) functions into the
		// loaded program; the report must list them, proving the probe sees the
		// whole flattened program, not just the entry module.
		rep := probe(t, "import \"std/array\";\nfunction main(): i32 { var xs: i32[] = [1, 2, 3]; return xs.len(); }", stdlibRoot)
		if !strings.Contains(rep, "module:") {
			t.Errorf("report missing verdict line\n--- report ---\n%s", rep)
		}
		if !strings.Contains(rep, "array__") {
			t.Errorf("report does not list mangled std/array functions — stdlib not loaded into the probe?\n--- report (head) ---\n%s", firstNLines(rep, 20))
		}
	})
}

// firstNLines returns the first n newline-delimited lines of s (for compact
// failure output on a large report).
func firstNLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
