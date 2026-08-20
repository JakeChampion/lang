package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The wasm `$__fern_str_split` / `$__fern_str_lines` helpers leaked their OWN
// scratch, independently of anything the reclaim passes can see.
//
// Three separate leaks, all inside the helper bodies:
//
//   - split built its result with the plain `$__fern_arr_push`, whose copy path
//     allocates a new buffer and abandons the old one. Every growth step
//     orphaned a buffer nothing could ever reach.
//   - split's initial array came from a bare `$__fern_alloc`, so it had no rc
//     header. `$__fern_arr_push` reads the rc word at [a-8] to choose in-place
//     versus copy; garbage there fails the test, so EVERY push copied — and the
//     header-less block itself was orphaned on the first one.
//   - lines allocated a "\n" delimiter block per call and never released it, and
//     dropped the trailing empty segment by DECREMENTING len, which puts that
//     element past the end where no element walk will ever reach it.
//
// Measured, 400 rounds of the harness below, a pair of compilers from the same
// commit:
//
//	shape                                wasm            x86-64 / arm64
//	18-part split                   67200 -> 0                       0
//	40-part split                  124800 -> 0                       0
//	char split (101 chars)         233600 -> 0                       0
//	lines, 4 lines + trailing \n    16000 -> 0                    9600
//	lines, no trailing \n            9600 -> 0                       0
//
// The fix is to make the helper own what it allocates: an rc-headered initial
// array, `$__fern_arr_push_owned` (which frees a superseded buffer at rc == 1)
// for the appends, and an explicit release of the delimiter and of the trimmed
// element. Nothing at the call site could have done any of this — the buffers
// are private to the helper and unreachable from the IR.
//
// The register column is untouched by the change and is here to keep it that
// way. Its one non-zero, `lines` with a trailing newline, is a PRE-EXISTING
// residue with a different cause: the register `__fern_str_lines` is Fern source
// (`asmcore.rt_src_str_lines`) that trims with `parts[0:keep]`, and the element
// the slice drops is not reclaimed. Same family, different mechanism, tracked
// separately — its ceiling below is deliberately slack so fixing it does not
// have to argue with a gate.

const wasmSplitScratchPrelude = `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
`

func wasmSplitScratchHeap(body string, limit int) string {
	return wasmSplitScratchPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
` + body + `
}
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= ` + fmt.Sprint(limit) + `) { return 98; }
    return 0;
}`
}

var wasmSplitScratchHeapCases = []struct {
	name            string
	body            string
	regMax, wasmMax int
}{
	{"wasm-split-scratch-18-parts", `    var parts: string[] = base.split("-");
    return parts.len();`, 4096, 4096},
	// Enough parts to cross several doublings, so the orphaned-buffer total is
	// dominated by growth rather than by the header-less first block.
	{"wasm-split-scratch-40-parts", wasmSplitScratch40Body, 4096, 4096},
	// The empty-separator branch has its own push site.
	{"wasm-split-scratch-char-split", `    var parts: string[] = base.split("");
    return parts.len() % 251;`, 4096, 4096},
	// Trailing newline: exercises the delimiter release AND the trimmed element.
	// The register ceiling is slack for the pre-existing rt_src_str_lines
	// residue described in the header — do not tighten it here.
	{"wasm-lines-scratch-trailing-newline", `    var doc: string = base + "\n" + base + "\n" + base + "\n";
    var ls: string[] = doc.lines();
    return ls.len();`, 16000, 4096},
	// No trailing newline: nothing is trimmed, so this isolates the delimiter.
	{"wasm-lines-scratch-no-trailing-newline", `    var doc: string = base + "\n" + base;
    var ls: string[] = doc.lines();
    return ls.len();`, 4096, 4096},
}

// wasmSplitScratch40Body is separated out only because a 40-part literal is
// unreadable inline.
const wasmSplitScratch40Body = `    var many: string = base + "-1-2-3-4-5-6-7-8-9-10-11-12-13-14-15-16-17-18-19-20-21-22-23-24-25-26-27-28-29-30-31-32-33-34-35-36-37-38-39-40";
    var parts: string[] = many.split("-");
    return parts.len() % 251;`

// wasmSplitScratchSemanticsSrc is the other half: releasing scratch must not
// change a single answer. Every case is checked against the register backends
// too, which run the same source through a different helper entirely.
const wasmSplitScratchSemanticsSrc = `function main(): i32 {
    var a: string[] = "a\nbb\n".lines();
    if (a.len() != 2) { return 1; }
    if (a[0] != "a" || a[1] != "bb") { return 2; }
    var b: string[] = "a\n\nc".lines();
    if (b.len() != 3) { return 3; }
    if (b[0] != "a" || b[1] != "" || b[2] != "c") { return 4; }
    var c: string[] = "x".lines();
    if (c.len() != 1 || c[0] != "x") { return 5; }
    var d: string[] = "".lines();
    if (d.len() != 0) { return 6; }
    var e: string[] = "a\n\n".lines();
    if (e.len() != 2) { return 7; }
    if (e[0] != "a" || e[1] != "") { return 8; }
    var f: string[] = "abc".split("");
    if (f.len() != 3 || f[0] != "a" || f[2] != "c") { return 9; }
    var g: string[] = "".split("");
    if (g.len() != 0) { return 10; }
    var h: string[] = "-a--b-".split("-");
    if (h.len() != 5) { return 11; }
    if (h[0] != "" || h[1] != "a" || h[2] != "" || h[3] != "b" || h[4] != "") { return 12; }
    var i2: string[] = "abc".split("|");
    if (i2.len() != 1 || i2[0] != "abc") { return 13; }
    var j: string[] = "aXXbXXc".split("XX");
    if (j.len() != 3 || j[1] != "b") { return 14; }
    var k: string[] = "1-2-3-4-5-6-7-8-9-10-11-12-13-14-15-16-17-18-19-20".split("-");
    if (k.len() != 20) { return 16; }
    if (k[0] != "1" || k[19] != "20" || k[9] != "10") { return 17; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`

const wasmSplitScratchExitHint = "98 = the helper's scratch was stranded; 99 = over-release; 97 = value corrupted; 1-17 = the semantics probe's own case numbers"

func wasmSplitScratchSources(wasm bool) []struct{ name, src string } {
	var out []struct{ name, src string }
	for _, tc := range wasmSplitScratchHeapCases {
		limit := tc.regMax
		if wasm {
			limit = tc.wasmMax
		}
		out = append(out, struct{ name, src string }{tc.name, wasmSplitScratchHeap(tc.body, limit)})
	}
	out = append(out, struct{ name, src string }{"wasm-split-scratch-semantics", wasmSplitScratchSemanticsSrc})
	return out
}

// TestSelfHostWasmSplitScratchWasmIR is the leg this change is for: every heap
// case fails with 98 on the parent.
func TestSelfHostWasmSplitScratchWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping split/lines scratch wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range wasmSplitScratchSources(true) {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, strings.ReplaceAll(tc.name, "/", "_")+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, got, wasmSplitScratchExitHint)
			}
		})
	}
}

// TestSelfHostWasmSplitScratchIRX86_64 keeps the register path honest: it runs
// the same sources through an entirely different set of helpers, so it pins that
// the wasm-side change moved nothing here and that the answers agree.
func TestSelfHostWasmSplitScratchIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range wasmSplitScratchSources(false) {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, wasmSplitScratchExitHint)
			}
		})
	}
}

// TestSelfHostWasmSplitScratchIRArm64 is the arm64 twin of the register leg.
func TestSelfHostWasmSplitScratchIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range wasmSplitScratchSources(false) {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, wasmSplitScratchExitHint)
			}
		})
	}
}
