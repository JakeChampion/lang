package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The SELF-ASSIGN record update `p = T { ...p, f: v }` reusing p's own box in
// place on the self-hosted stack-IR path (#6134 — the port of native's
// tryStructReuseOverwrite spread admission, #6124). E048 forbids field
// assignment, so this is how every struct mutation in Fern is written; before
// the port each one copied into a fresh box.
//
// Two contracts per case:
//   - the exit code pins VALUE correctness. A reuse that wrote a field before
//     an override had read it, freed a field the new value aliased, or fired
//     on an aliased box would corrupt or crash here;
//   - the emitted call targets pin the EMISSION contract, which is the only
//     instrument that can see this change: allocation COUNT is what moves, and
//     both `__heap_bump_bytes()` and the append-cliff counter are blind to it
//     (the freed boxes are recycled by the freelist and no appends are
//     involved). An admitted update emits `__fern_alloc_reuse`; a refused one
//     emits `__fern_struct_copy` into a fresh box.
//
// The controls are the point. `self-assign-aliased`, `escapes-call`,
// `escapes-container` and `plain-rebind` must each stay on struct_copy — the
// admission set is what keeps the reuse arm's old-field release alias-free.
var selfAssignReuseCases = []struct {
	name       string
	src        string
	expected   int
	reuse      int // user-code `call __fn___fern_alloc_reuse` sites
	structCopy int // user-code `call __fn___fern_struct_copy` sites
}{
	// The idiom itself, in a loop: one reuse site, zero copies. p.x ends at 4,
	// p.y is carried untouched at 7.
	{"self-assign-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 0, y: 7 }; var i: i32 = 0; while (i < 4) { p = P { ...p, x: p.x + 1 }; i = i + 1; } return p.x + p.y; }`,
		11, 1, 0},
	// An override that READS the base is the whole point of the port: the
	// rebind family refuses these outright ("no override value may reference
	// d"), and they are what every real record update looks like. Sound because
	// emit_self_overwrite_reuse evaluates every override into a temp before the
	// box is touched. x doubles each turn from 7: 7, 14, 21, 28.
	{"self-assign-reads-base",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 0, y: 7 }; var i: i32 = 0; while (i < 4) { p = P { ...p, x: p.x + p.y }; i = i + 1; } return p.x % 200; }`,
		28, 1, 0},
	// The sharpest read-before-write case: a field SWAP. Writing x before
	// reading y (or vice versa) yields 11 or 22 instead of 21.
	{"self-assign-field-swap",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 1, y: 2 }; p = P { ...p, x: p.y, y: p.x }; return p.x * 10 + p.y; }`,
		21, 1, 0},
	// ALIAS control: `q` binds p's box, so p is not the sole owner and the
	// update must stay on the copy path — q keeps x=1.
	{"self-assign-aliased",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 1, y: 2 }; var q: P = p; p = P { ...p, x: 9 }; return q.x * 10 + p.x + q.y; }`,
		21, 0, 1},
	// ESCAPE control: p is passed to a function, so the callee could retain it.
	{"self-assign-escapes-call",
		`struct P { x: i32, y: i32 } function take(q: P): i32 { return q.x + q.y; } function main(): i32 { var p: P = P { x: 1, y: 2 }; p = P { ...p, x: 5 }; return take(p); }`,
		7, 0, 1},
	// ESCAPE control: p is moved into a container, which owns the box from
	// there on. A reuse would write straight through ps[0].
	{"self-assign-escapes-container",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 1, y: 2 }; var ps: P[] = []; p = P { ...p, x: 5 }; ps = ps.append(p); p = P { ...p, x: 9 }; return ps[0].x * 10 + p.x; }`,
		59, 0, 2},
	// MIXED-ASSIGNMENT control: one plain rebind among the updates disqualifies
	// the name entirely. The rebind's field values carry no freshness gate, so
	// the box it leaves behind is not one the next update's release arm can
	// reason about.
	{"self-assign-plain-rebind",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 1, y: 2 }; p = P { ...p, x: 5 }; p = P { x: 100, y: 200 }; p = P { ...p, y: p.y + 1 }; return (p.x + p.y) % 200; }`,
		101, 0, 2},
	// An ARRAY field override (a fresh literal — an aliased one is refused):
	// the reuse arm releases the old buffer before storing the new one, so a
	// leak or a double-free would show up in the 5-iteration value. n
	// accumulates p.xs[0] read BEFORE the overwrite: 0+0+1+2+3 = 6, plus
	// p.xs[0]=4 and p.xs[1]=5. Three box sites: the two array literals plus
	// the initial struct box (the updates reuse it).
	{"self-assign-array-field",
		`struct P { xs: i32[], n: i32 } function main(): i32 { var p: P = P { xs: [1, 2, 3], n: 0 }; var i: i32 = 0; while (i < 5) { p = P { ...p, xs: [i, i + 1], n: p.n + p.xs[0] }; i = i + 1; } return p.n + p.xs[0] + p.xs[1]; }`,
		16, 1, 0},
	// Updates in BOTH arms of an if nested in a loop: each is its own reuse
	// site against the same slot.
	{"self-assign-branch-arms",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 1, y: 2 }; var i: i32 = 0; while (i < 3) { if (i > 0) { p = P { ...p, x: p.x + p.y }; } else { p = P { ...p, y: p.y + 1 }; } i = i + 1; } return p.x * 10 + p.y; }`,
		73, 2, 0},
	// A NESTED-STRUCT field override: the reuse arm full-freeing-drops the old
	// inner box before the fresh one is stored, and the override reads through
	// the very field being replaced (`p.inner.a + 1`).
	{"self-assign-nested-struct",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { var p: Outer = Outer { inner: Inner { a: 1, b: 2 }, n: 0 }; var i: i32 = 0; while (i < 4) { p = Outer { ...p, inner: Inner { a: p.inner.a + 1, b: 3 }, n: p.n + p.inner.b }; i = i + 1; } return p.inner.a * 10 + p.n; }`,
		61, 1, 0},
	// SHADOWING control: `for p in ps` rebinds the name to an ELEMENT of ps, a
	// box the container owns. slot_of resolves to that binding, so admitting the
	// name would fire the reuse there and write straight through ps[0] —
	// ps[0].xs[0] must stay 7.
	{"self-assign-shadowed-by-for",
		`struct P { xs: i32[], n: i32 } function main(): i32 { var p: P = P { xs: [1], n: 0 }; var ps: P[] = [P { xs: [7, 8], n: 5 }]; for p in ps { p = P { ...p, xs: [2], n: p.n + 1 }; } return ps[0].xs[0] * 10 + ps[0].n + p.n; }`,
		75, 0, 1},
	// SHADOWING control: a nested `var p` of the same type. Only the top-level
	// binding carries the donor gates, so the name is refused outright.
	{"self-assign-shadowed-by-var",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 1, y: 2 }; var i: i32 = 0; while (i < 2) { var p: P = P { x: 5, y: 6 }; p = P { ...p, x: p.x + 1 }; i = i + 1; } p = P { ...p, x: p.x + 10 }; return p.x * 10 + p.y; }`,
		112, 0, 2},
	// Memory safety at scale: 5M updates through one box. A per-iteration leak
	// would exhaust the arena (exit 125) and a double-free would crash.
	{"self-assign-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 0, y: 1 }; var i: i32 = 0; while (i < 5000000) { p = P { ...p, x: (p.x + p.y) % 1000 }; i = i + 1; } return p.x % 200; }`,
		0, 1, 0},
	// Same at scale for the array-field shape, where each turn also frees a
	// buffer and allocates its replacement.
	{"self-assign-array-churn-safe",
		`struct P { xs: i32[], n: i32 } function main(): i32 { var p: P = P { xs: [1], n: 0 }; var i: i32 = 0; while (i < 200000) { p = P { ...p, xs: [i, i + 1, i + 2], n: (p.n + p.xs[0]) % 977 }; i = i + 1; } return p.n % 200; }`,
		8, 1, 0},
}

// TestSelfHostSelfAssignReuseIRX86_64 compiles each case through the self-hosted
// x86-64 driver (asm_run, IR default-on), asserting the exit code plus the
// reuse-vs-copy emission contract.
func TestSelfHostSelfAssignReuseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range selfAssignReuseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if got := countUserCalls(asm, "__fn___fern_alloc_reuse"); got != tc.reuse {
				t.Errorf("%s: %d __fern_alloc_reuse sites, want %d — the record-update admission set moved", tc.name, got, tc.reuse)
			}
			if got := countUserCalls(asm, "__fn___fern_struct_copy"); got != tc.structCopy {
				t.Errorf("%s: %d __fern_struct_copy sites, want %d — an update that should have reused copied into a fresh box (or one that must copy stopped)", tc.name, got, tc.structCopy)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostSelfAssignReuseWasmIR runs the same programs through the
// self-hosted WASM IR backend. The admission set lives in the shared irlower, so
// this leg is the value contract on a second backend — the emission counts are
// asserted on x86-64 above. Exit codes stay < 126 for WASI's _start range.
func TestSelfHostSelfAssignReuseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host self-assign-reuse wasm IR e2e")
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

	for _, tc := range selfAssignReuseCases {
		if strings.HasSuffix(tc.name, "churn-safe") {
			continue // multi-million-iteration cases: x86-64 carries the scale contract
		}
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("self-assign reuse wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
