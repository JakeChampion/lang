package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A bare-ident match SCRUTINEE is a borrow in the enum-field walk too ------
//
// An enum local that is not matched exactly ONCE at top level reclaimed nothing:
// matched twice, matched inside an `if`, matched inside a loop — all 200 allocs
// / 0 frees over 100 rounds against native's 200/200.
//
// collect_fresh_rcenum_names takes its match-consumed branch only when
// sole_top_level_match_idx finds a single top-level match on the name. Every
// other shape falls to body_unsafe_for_enumfield, and ef_unsafe_stmt's StmtMatch
// arm sent its scrutinee to ef_unsafe_expr, whose default arm reaches the STRICT
// walker — which reads a bare ident as an escape. stmt_unsafe_for_match_borrow
// has read a bare-ident scrutinee as a BORROW all along; this fork never did.
//
// Matching a value does not hand it out. The arms' payload bindings are the part
// that can — and on THIS branch nothing refuses them, because the arm body
// mentions the binding rather than the matched name. It is sound anyway: the
// binding's own assignment retains, so both owners are counted. Two rows below
// establish that, one on the counts and one on the VALUE with allocation churn
// after the match, because counts and the underflow guard cannot see a
// use-after-READ.
//
// Every want was confirmed against the native x86-64 backend. Exit 99 is
// reserved for __rc_underflow_count().

type enumScrutineeCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const escrMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func enumScrutineeCases() []enumScrutineeCase {
	const decls = `enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
`
	return []enumScrutineeCase{
		{
			// Two top-level matches, so sole_top_level_match_idx declines and the
			// escape walk decides alone. Base: 200 allocs / 0 frees.
			name: "matched_twice",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    let a: i32 = 0;
    let b: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len(); }, E.B => { a = 0; } }
    match (v) { E.A(xs) => { b = xs.len(); }, E.B => { b = 0; } }
    return (a + b) % 101;
}
` + escrMain,
			want: 12, allocs: 200, frees: 200,
		},
		{
			// Matched inside a conditional — not a TOP-LEVEL match, so the same
			// branch decides. Base: 200 / 0.
			name: "matched_in_if",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    let a: i32 = 0;
    if (i % 2 == 0) { match (v) { E.A(xs) => { a = xs.len(); }, E.B => { a = 0; } } }
    return a % 101;
}
` + escrMain,
			want: 3, allocs: 200, frees: 200,
		},
		{
			// Matched inside a loop, so the same box is read on every iteration.
			// Base: 200 / 0.
			name: "matched_in_while",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    let a: i32 = 0;
    let k: i32 = 0;
    while (k < 2) { match (v) { E.A(xs) => { a = a + xs.len(); }, E.B => { a = a; } } k = k + 1; }
    return a % 101;
}
` + escrMain,
			want: 12, allocs: 200, frees: 200,
		},
		{
			// A non-sole match whose arm binds the payload OUT: `keep = xs`
			// retains the payload, so there are two counted owners and the
			// sweep leaves `keep` holding one.
			name: "non_sole_match_binds_payload_out",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    let keep: i32[] = [0];
    if (i % 2 == 0) { match (v) { E.A(xs) => { keep = xs; }, E.B => { keep = [0]; } } }
    return (keep.len() + keep[0]) % 101;
}
` + escrMain,
			want: 78, allocs: 200, frees: 200,
		},
		{
			// The same hazard read as a VALUE after the match. Counts and the
			// underflow guard cannot see a use-after-READ; the value can. Native
			// returns 53; so do all three backends here. The arrays after the
			// match are constant and not heap-allocated on the typed lowering.
			// The payload reaches its variant through `id`, which hides the constant from the static-box plan,
			// so the variant is still allocated.
			//
			// The modulus is 97 rather than something larger because WASI rejects
			// an exit status outside [0, 126) and the wasm leg reads the value
			// through the exit code.
			name: "payload_read_back_after_churn",
			src: `enum E { A(i32[]), B }
function id(xs: i32[]): i32[] { return xs; }
function mkv(): E { return E.A(id([7, 8])); }
function round(i: i32): i32 {
    let v: E = mkv();
    let keep: i32[] = [0];
    if (i % 2 == 0) { match (v) { E.A(xs) => { keep = xs; }, E.B => { keep = [0]; } } }
    let j1: i32[] = [111, 222];
    let j2: i32[] = [333, 444];
    let j3: i32[] = [555, 666];
    return keep[0] + keep[keep.len() - 1] + j1[0] - j1[0] + j2[0] - j2[0] + j3[0] - j3[0];
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 20) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 53, allocs: 20, frees: 20,
		},
		{
			// The SOLE top-level match binding its payload out: `keep = xs`
			// takes a counted claim of its own, so every box and payload is
			// reclaimed.
			name: "sole_match_binds_payload_out_reclaimed",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    let keep: i32[] = [0];
    match (v) { E.A(xs) => { keep = xs; }, E.B => { keep = [0]; } }
    return (keep.len() + keep[0]) % 101;
}
` + escrMain,
			want: 5, allocs: 200, frees: 200,
		},
		{
			// The single top-level match on an INLINE ctor bind, which takes the
			// match-consumed branch instead and was already flat. Kept as the
			// control for that branch, since this slice must not disturb it.
			name: "inline_ctor_sole_match_unchanged",
			src: decls + `function round(i: i32): i32 {
    let v: E = E.A([i, i + 1]);
    let a: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len(); }, E.B => { a = 0; } }
    return a % 101;
}
` + escrMain,
			want: 6, allocs: 200, frees: 200,
		},
	}
}

// TestSelfHostEnumMatchScrutineeBorrowX86_64 is the leak-accounting leg.
func TestSelfHostEnumMatchScrutineeBorrowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range enumScrutineeCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "enumscrut_"+tc.name, asm)
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
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostEnumMatchScrutineeBorrowWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostEnumMatchScrutineeBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping enum match-scrutinee borrow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range enumScrutineeCases() {
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
			watFile := filepath.Join(dir, "enumscrut_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("enum match-scrutinee borrow wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostEnumMatchScrutineeBorrowIRArm64 — the arm64 sibling under qemu.
func TestSelfHostEnumMatchScrutineeBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range enumScrutineeCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "enumscrut_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
