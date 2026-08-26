package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- Aliasing a param does not hand it out ----------------------------------
//
// A callee that does nothing but ALIAS its parameter marked that parameter
// non-borrowable, and every CALLER then refused its own release. Measured
// against one caller over 20 rounds, with the callee releasing nothing in any
// case:
//
//	return p.len() + o.len();            caller 80/80  — flat
//	var q: string = p; ... q.len() ...   caller 80/40
//	var q: string = p; q = o; ...        caller 80/0
//
// The whole loss is caller-side, the same expensive half #7507 found when a
// callee read its param through a match expression.
//
// borrowable_params_interproc's per-param gate calls the escape walker with an
// EMPTY alias_ok, so `var q = p` reads as a bare-ident escape — the same
// asymmetry #7512 closed one layer down for the string credit. Two readings are
// now admitted as a UNION of independent proofs: the first forgives a bare-ident
// match scrutinee and stays strict on aliases, the second forgives non-escaping
// aliases and stays strict on scrutinees. A body with BOTH satisfies neither and
// is still refused, so this does not widen past what either walker proves.
//
// Two things the param verdict needs that the local-reclaim analyses do not:
//
//   - the alias sites are collected WITHOUT alias_bind_sites_of's reassigned-
//     target exclusion. That exclusion protects a LOCAL's reclaim credit, where a
//     slot reassigned before its sweep would release the wrong box. The question
//     here is only whether the PARAM escapes, and `var q = p; q = o;` answers it:
//     q briefly aliases p, then stops naming it.
//   - the REASSIGN sites are collected too. `var q = p; q = o;` escapes p through
//     the bind and o through the assignment; forgiving only the bind left the
//     other caller half still refused, measured at 80/40.
//
// The row that carries the soundness is the CALLER-side value probe. Every other
// failure in this wave was observable from inside the program under test — a leak
// in the counts, an over-release in __rc_underflow_count(), a use-after-read in
// the same function. Freeing a borrowed param's box corrupts memory the CALLER
// owns and the callee exits clean, so that row reads the caller's own strings
// back after the call with three fresh allocations in between.
//
// Every want was confirmed against the native x86-64 backend. Native const-folds
// these literal concats and allocates nothing, so its COUNTS are not a comparison
// — its ANSWERS are, and they match on every row. Exit 99 is reserved for
// __rc_underflow_count().

type aliasedParamCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const apbMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

const apbCaller = `function round(i: i32): i32 {
    var a: string = "ab" + "cd";
    var b: string = "ef" + "gh";
    return consume(a, b) + a.len() - a.len();
}
`

func aliasedParamCases() []aliasedParamCase {
	return []aliasedParamCase{
		{
			// The control that was already flat: params read directly, no alias.
			// It is what establishes the other rows are about the ALIAS and not
			// about passing a string at all.
			name: "params_read_directly",
			src: `function consume(p: string, o: string): i32 { return p.len() + o.len(); }
` + apbCaller + apbMain,
			want: 63, allocs: 80, frees: 80,
		},
		{
			// One param aliased into a local. Base: 80/40 — the caller reclaims
			// `b` (whose param is only read) and leaks `a`.
			name: "param_aliased_to_local",
			src: `function consume(p: string, o: string): i32 { var q: string = p; return q.len() + o.len(); }
` + apbCaller + apbMain,
			want: 63, allocs: 80, frees: 80,
		},
		{
			// Both params aliased — one by the bind, one by the reassign. Base:
			// 80/0, the caller reclaiming neither. This is the row that needs the
			// reassign sites; with the bind arm alone it sits at 80/40.
			name: "param_aliased_then_reassigned",
			src: `function consume(p: string, o: string): i32 { var q: string = p; q = o; return q.len() + p.len(); }
` + apbCaller + apbMain,
			want: 63, allocs: 80, frees: 80,
		},
		{
			// THE SOUNDNESS ROW. The caller reads ITS OWN strings back after the
			// call, with three fresh allocations in between so a freed box would
			// be reused before the read. If the callee ever released a borrowed
			// param's box this is the only place it shows — the callee exits
			// clean either way. Native returns 7. Base: 200/120.
			name: "caller_reads_params_back_after_churn",
			src: `function consume(p: string, o: string): i32 {
    var q: string = p;
    q = o;
    return q.len() + p.len();
}
function round(i: i32): i32 {
    var a: string = "ab" + "cd";
    var b: string = "efg" + "hij";
    var n: i32 = consume(a, b);
    var j1: string = "pp" + "qq";
    var j2: string = "rr" + "ss";
    var j3: string = "tt" + "uu";
    return n + a.len() * 7 + (a[0] as i32) + b.len()
        + j1.len() - j1.len() + j2.len() - j2.len() + j3.len() - j3.len();
}
` + apbMain,
			want: 7, allocs: 200, frees: 200,
		},
		{
			// THE NEGATIVE CONTROL, and the reason this is a carve-out rather than
			// a blanket accept: the alias ESCAPES by return, so `p` really is
			// handed out and its param must stay non-borrowable. 80/40 before and
			// after — the caller keeps leaking `a`, which is the safe direction.
			//
			// If this row ever reaches 80/80, the union admitted a body whose
			// alias leaves the function, and the caller is now releasing a string
			// its callee returned.
			name: "escaping_alias_keeps_param_refused",
			src: `function consume(p: string, o: string): string {
    var q: string = p;
    return q;
}
function round(i: i32): i32 {
    var a: string = "ab" + "cd";
    var b: string = "ef" + "gh";
    var r: string = consume(a, b);
    return r.len() + a.len() + b.len();
}
` + apbMain,
			want: 46, allocs: 80, frees: 40,
		},
		{
			// The string-builder accumulator, which reaches these predicates by a
			// different route (collect_str_accumulator_names / emit_str_reclaim_store)
			// and must not move.
			name: "string_accumulator_unchanged",
			src: `function round(i: i32): i32 {
    var s: string = "";
    var k: i32 = 0;
    while (k < 4) { s = s + "ab"; k = k + 1; }
    return s.len();
}
` + apbMain,
			want: 63, allocs: 160, frees: 160,
		},
	}
}

// TestSelfHostAliasedParamBorrowX86_64 is the leak-accounting leg.
func TestSelfHostAliasedParamBorrowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range aliasedParamCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "apb_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; a CHANGED value on the "+
					"churn row means the callee freed a box its caller still owns)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d. FEWER means the aliased param stopped "+
					"being borrowable and its callers lost their release again; MORE on the "+
					"escaping row means the union admitted an alias that leaves the function",
					tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostAliasedParamBorrowWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostAliasedParamBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping aliased-param borrow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range aliasedParamCases() {
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
			watFile := filepath.Join(dir, "apb_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("aliased-param borrow wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostAliasedParamBorrowIRArm64 — the arm64 sibling under qemu.
func TestSelfHostAliasedParamBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range aliasedParamCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "apb_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
