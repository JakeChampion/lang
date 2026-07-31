package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A MAIN-LESS module — a library compiled standalone — now lowers on the
// self-host IR path (#3457 slice 5). It used to be a blanket refusal:
// emit_module_ir_unit's `_start` emitted `call __fn_main` unconditionally, so the
// eligibility gate carried a `require_main` flag that bailed any such module to
// the legacy AST emitter. `_start` now exits 0 instead when there is no main, and
// the flag is gone.
//
// This is not a hypothetical shape. TestSelfHostBootstrapsItself pipes each
// compiler SOURCE through the same driver, and 7 of its 9 files are main-less
// libraries (util / astwalk / asmcore / ir / irlower / asm_ir; only lexer, parser
// and asm define `main`) — so that test was compiling most of the compiler
// through the emitter #3457 exists to delete. NOTE the doc's gap-reason table
// listed `no-main` as "supported by the desugar; not exercised today", which was
// wrong on both counts.
//
// A script-shaped source (top-level statements, no `main`) is NOT this case:
// asmcore.synth_script_main rewrites it into `function main()` upstream, which
// TestSelfHostScriptMainIRX86_64 covers. Anything still main-less here is a
// library with functions only.
var noMainModuleCases = []struct {
	name string
	src  string
}{
	// The minimal shape: one exported function, nothing calls it.
	{"single-fn", `pub function add(a: i32, b: i32): i32 { return a + b; }`},
	// Several functions, one calling another — the intra-module call must still
	// resolve with no entry point driving it.
	{"internal-call", `
function twice(n: i32): i32 { return n * 2; }
pub function quad(n: i32): i32 { return twice(twice(n)); }`},
	// Heap + string work, so the module pulls in the runtime (alloc / str_concat)
	// and `_start` is emitted alongside it. A library whose functions allocate is
	// the common real case (util.fern, asmcore.fern).
	{"string-heap", `
pub function label(i: i32): string {
    if (i % 2 == 0) { return "ab" + "!"; }
    return "xyz";
}
pub function total(xs: i32[]): i32 {
    var t: i32 = 0;
    for x in xs { t = t + x; }
    return t;
}`},
	// A struct + an enum decl with methods: the shapes whose layout/shape pools
	// `_start`'s surrounding literal pool has to emit even with no main.
	{"struct-and-enum", `
struct P { name: string, n: i32 }
enum C { Red, Blue }
pub function pick(p: P): i32 { return p.n + p.name.len(); }
pub function code(c: C): i32 { match (c) { Red => { return 1; }, Blue => { return 2; } } }`},
}

// TestSelfHostNoMainModuleIRX86_64 asserts each main-less module (a) routes "ir",
// (b) emits asm that assembles, and (c) links and RUNS to exit 0 — `_start`
// exiting 0 directly is the whole behavioural contract of the no-main branch, and
// running it is what distinguishes "exits 0" from "jumps to an absent __fn_main".
func TestSelfHostNoMainModuleIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range noMainModuleCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(strings.TrimLeft(tc.src, "\n") + "\n")
			if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src))); path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			// `_start` must exit 0 rather than call a __fn_main that isn't there.
			if strings.Contains(string(asm), "call __fn_main") {
				t.Errorf("%s: emitted `call __fn_main` for a module with no main", tc.name)
			}
			progBin := buildBin(t, gcc, dir, "nomain_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 (a main-less module's _start exits 0)", tc.name, code)
			}
		})
	}

	// The real consumer, in miniature: a main-less compiler SOURCE. util.fern is
	// the first file TestSelfHostBootstrapsItself pipes through this driver, and
	// the one that failed when the driver was rerouted IR-or-error.
	t.Run("util-fern-routes-ir", func(t *testing.T) {
		src, err := os.ReadFile("../../examples/self_host/util.fern")
		if err != nil {
			t.Fatalf("read util.fern: %v", err)
		}
		if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src))); path != "ir" {
			t.Fatalf("util.fern routed through %q path, want \"ir\"", path)
		}
		if asm := runCapture(t, gcc, runner, driverBin, src); len(asm) == 0 {
			t.Fatal("util.fern emitted 0 bytes")
		}
	})
}
