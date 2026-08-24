package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A value block is not a closure -----------------------------------------
//
// Reading a struct local's field through a `match` EXPRESSION cost that local —
// and every other struct local in the same function — its reclaim credit, so
// nothing was released at all, not even the holder box. The same code with a
// plain field read was flat.
//
// A match expression is not an AST expression: parser.fern desugars it to a
// zero-arg IIFE marked ORIGIN_MATCH_EXPR, one of the VALUE BLOCK origins irlower
// INLINES rather than calls, so no closure is ever built. expr_unsafe_for's
// ExprLambda arm did not know that and read every ident in the body as a
// capture. From there the credit machinery worked correctly on a false premise:
// alias_bind_sites_of refuses an escaping alias, and #7282's rule that the
// forgiveness and the credit-copy must agree then withdrew the SOURCE's credit
// too, leaving neither box released.
//
// The rows pair each value-block form with the identical program written
// without one, so a regression shows up as the pair diverging rather than as a
// number nobody can place. `if` expressions and block expressions ride the same
// origin set as `match` and are pinned here for that reason.
//
// Exit 99 is reserved for an over-release on every row: the narrowing must not
// start freeing a value a real capture still holds, which is what the
// real_lambda_capture_refused row guards from the other side.
//
// Every want was confirmed against the native x86-64 backend and bin/fern
// -interp, never read off the self-host run.

type matchExprBorrowCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const mebMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func matchExprBorrowCases() []matchExprBorrowCase {
	const enumDecls = `struct P { f: E, n: i32 }
enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
`
	const arrDecls = `struct Q { xs: i32[], n: i32 }
function mkxs(i: i32): i32[] { var o: i32[] = [i, i + 1]; return o; }
`
	return []matchExprBorrowCase{
		{
			// THE REPRO. The match expression reads a field of an ALIASED struct
			// local. Base: allocs=300 frees=0 — the payload, the enum box and the
			// holder box all stranded, every round.
			name: "match_expr_field_read_aliased",
			src: enumDecls + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = q;
    return ((match (p.f) { E.A(xs) => xs.len(), E.B => 0 }) + p.n) % 101;
}
` + mebMain,
			want: 5, allocs: 300, frees: 300,
		},
		{
			// The control: byte-identical but for the field read, which is what
			// makes the row above attributable. This one was ALWAYS clean.
			name: "plain_field_read_aliased",
			src: enumDecls + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = q;
    return p.n % 101;
}
` + mebMain,
			want: 3, allocs: 300, frees: 300,
		},
		{
			// No alias in sight: a match expression over a plain local's field.
			// The poisoning was never about the alias — the alias bind is just
			// where the refusal became visible.
			name: "match_expr_no_alias",
			src: enumDecls + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    return ((match (q.f) { E.A(xs) => xs.len(), E.B => 0 }) + q.n) % 101;
}
` + mebMain,
			want: 5, allocs: 300, frees: 300,
		},
		{
			// An IF expression rides the same value-block origin set, so it was
			// poisoning identically — with an array-field holder, to show the
			// field kind was never what mattered.
			name: "if_expr_field_read_aliased",
			src: arrDecls + `function round(i: i32): i32 {
    var q: Q = Q { xs: mkxs(i), n: i };
    var p: Q = q;
    return ((if (p.n % 2 == 0) { p.xs.len() } else { 0 }) + p.n) % 101;
}
` + mebMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// A block expression, the third origin in the set.
			name: "block_expr_field_read_aliased",
			src: arrDecls + `function round(i: i32): i32 {
    var q: Q = Q { xs: mkxs(i), n: i };
    var p: Q = q;
    return (({ var k: i32 = p.xs.len(); k + 1 }) + p.n) % 101;
}
` + mebMain,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// The same bug one KIND over, and the reason the fix is not confined
			// to expr_unsafe_for: strarr_expr_unsafe carried the identical blanket
			// test, so a string[] local read through a match expression stranded
			// its elements and buffer. Base: allocs=500 frees=100.
			name: "match_expr_strarr_read",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function round(i: i32): i32 {
    var xs: string[] = [w("a"), w("b")];
    return (match (xs.len()) { 2 => xs[0].len(), _ => 0 }) % 101;
}
` + mebMain,
			want: 93, allocs: 500, frees: 500,
		},
		{
			// Its control: the same program with a plain element read, always
			// clean. The pair is what makes the row above attributable.
			name: "plain_strarr_read",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function round(i: i32): i32 {
    var xs: string[] = [w("a"), w("b")];
    return xs[0].len() % 101;
}
` + mebMain,
			want: 93, allocs: 500, frees: 500,
		},
		{
			// REFUSED, and must stay refused: a REAL lambda whose body mentions
			// the local may run after this frame, so the blanket capture test
			// still applies and the local keeps leaking rather than being freed
			// under a live closure. This is the row that fails loudly (99, or a
			// free count that climbs) if the narrowing is ever widened past the
			// value-block origins.
			name: "real_lambda_capture_refused",
			src: arrDecls + `function apply(f: () => i32): i32 { return f(); }
function round(i: i32): i32 {
    var q: Q = Q { xs: mkxs(i), n: i };
    var p: Q = q;
    return (apply(() => p.xs.len() + p.n)) % 101;
}
` + mebMain,
			want: 5, allocs: 300, frees: 0,
		},
	}
}

// TestSelfHostMatchExprBorrowX86_64 is the leak-accounting leg.
func TestSelfHostMatchExprBorrowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range matchExprBorrowCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "matchexprborrow_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a release ran "+
					"under a live claim)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER on a value-block row means the "+
					"IIFE body reads as a capture again; MORE on the real-lambda row "+
					"means the narrowing reached past the value-block origins",
					tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostMatchExprBorrowWasmIR — the wasm sibling. Exit codes only, so what
// this leg catches is a release that frees a LIVE box on wasm.
func TestSelfHostMatchExprBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping match-expr borrow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range matchExprBorrowCases() {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "matchexprborrow_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("match-expr borrow wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostMatchExprBorrowIRArm64 — the arm64 sibling under qemu.
func TestSelfHostMatchExprBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range matchExprBorrowCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "matchexprborrow_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
