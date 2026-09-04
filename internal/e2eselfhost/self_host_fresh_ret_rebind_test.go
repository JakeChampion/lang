package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The REBOUND fresh-ret-call struct local ---------------------------------
//
// `var s: S = mk(i); s = mk(i + 1);` — a struct local bound from a producer call
// and then reassigned. collect_fresh_ret_call_names used to drop every name in
// body_assign_targets, so this local earned no credit at all: `round` emitted no
// dec of any kind, and the box plus its rc fields leaked on every rebind
// (800 allocs / 0 frees over 200 rounds, against native's 800/800).
//
// The exclusion deferred to the snapshot-LOCAL path, which claims only the
// locals threaded and MOVED OUT (`var st = f(x); st = st.emit(..); return st`).
// A rebound local that simply goes dead fell between the two.
//
// The INIT spelling is what decided it, not the rebind: the literal-bound
// sibling (collect_fresh_struct_names) never carried the exclusion, so
// `var s: S = S { .. }` was already clean under the identical rebind. That
// asymmetry is why `literal_init_control` sits here — it passed before the fix
// and pins the half that was never broken.
//
// It is also why no cell of the generated leak matrix could see this: every
// kind there inits from a literal (`var x: P = P { xs: [i, i + 1], k: i }`),
// so its `rebind` scope exercises only the clean spelling. A row added there
// would not have caught it either.
//
// THREE SHAPES STAY REFUSED, and each is an escape the credit must not reach —
// they keep their pre-existing leak deliberately, so they assert their exit code
// only, never balance. If one starts balancing, a gate that declines it has
// stopped firing:
//
//   - `alias_into_container`: the old value is appended to a live `S[]` before
//     the rebind, so releasing at the rebind would free a box the container
//     still points at.
//   - `field_moved_out`: `var held: string = s.name` carries a field out of the
//     box the rebind orphans; the deep drop would free a live buffer.
//   - `rebind_then_return`: the final value escapes, so the frame owns neither
//     end of the chain.
//
// Every `want` was measured against the NATIVE x86-64 backend, never read off
// the self-host run under test. The U-shaped rows are wrong-ANSWER probes as
// well as census rows: each reads its held string back after `churn` has had the
// chance to reuse anything freed too early, and returns -1 on a short read, so
// the run answers 100 rather than balancing quietly. The census alone cannot
// separate a correct fix from an over-release — both read allocs == frees.

const freshRetRebindDecl = `struct S { name: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mk(i: i32): S { return S { name: w("k"), n: i }; }
function churn(i: i32): i32 {
    var a: string = w("chunkA");
    var b: string = w("chunkB");
    return a.len() + b.len() + i;
}
`

// The plain driver: 200 rounds, no readback assertion.
const freshRetRebindMain = `
function main(): i32 {
    var acc: i32 = 0; var r: i32 = 0;
    while (r < 200) { acc = acc + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

// The readback driver: a round returning -1 (a string that came back short)
// answers 100, so an over-release is a wrong ANSWER and not just a count.
const freshRetRebindCheckedMain = `
function main(): i32 {
    var acc: i32 = 0; var r: i32 = 0; var bad: i32 = 0;
    while (r < 200) { var v: i32 = round(r); if (v < 0) { bad = bad + 1; } acc = acc + v; r = r + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

type freshRetRebindCase struct {
	name string
	src  string
	want int
	// balance: the run must end at live_bytes 0 with allocs == frees. False for
	// the three escape shapes the credit is refused for — they keep their
	// pre-existing leak, and asserting balance would pin the wrong behaviour.
	balance bool
}

func freshRetRebindCases() []freshRetRebindCase {
	return []freshRetRebindCase{
		{
			// The shape: producer-call init, producer-call rebind.
			name: "call_init_rebind",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    var s: S = mk(i);
    s = mk(i + 1);
    return (s.name.len() + s.n) % 101;
}` + freshRetRebindMain,
			want: 79, balance: true,
		},
		{
			// Same local, rebound from a struct LITERAL instead. The init is what
			// was excluded, so both rebind spellings leaked identically.
			name: "call_init_literal_rebind",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    var s: S = mk(i);
    s = S { name: w("z"), n: i + 1 };
    return (s.name.len() + s.n) % 101;
}` + freshRetRebindMain,
			want: 79, balance: true,
		},
		{
			// The control that was ALWAYS clean: literal init under the identical
			// rebind. It is the row that localises a regression to the collector
			// this change touched rather than to the shared gate below it.
			name: "literal_init_control",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    var s: S = S { name: w("k"), n: i };
    s = S { name: w("z"), n: i + 1 };
    return (s.name.len() + s.n) % 101;
}` + freshRetRebindMain,
			want: 79, balance: true,
		},
		{
			// A struct with NO rc fields at all: the box itself is what leaked,
			// freed by a shallow dec. Its producer allocates a string internally,
			// so the census still has something to count.
			name: "scalar_struct_rebind",
			src: `struct N { a: i32, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkn(i: i32): N { var junk: string = w("x"); return N { a: junk.len(), n: i }; }
function round(i: i32): i32 {
    var s: N = mkn(i);
    s = mkn(i + 1);
    return (s.a + s.n) % 101;
}` + freshRetRebindMain,
			want: 79, balance: true,
		},
		{
			// An ARRAY field: the rebind must reach the deep field drop, not just
			// the box dec — 1600/400 before.
			name: "arr_field_rebind",
			src: `struct A { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mka(i: i32): A { var o: string[] = []; o = o.append(w("a")); return A { f: o, n: i }; }
function churn(i: i32): i32 { var a: string = w("chunkA"); var b: string = w("chunkB"); return a.len() + b.len() + i; }
function round(i: i32): i32 {
    var s: A = mka(i);
    s = mka(i + 1);
    var j: i32 = churn(i);
    if (s.f[0].len() != 31) { return 0 - 1; }
    return (s.f.len() + s.n + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 81, balance: true,
		},
		{
			// Three generations inside a loop, so the rebind release runs on a box
			// the previous iteration produced rather than on the init's.
			name: "loop_rebind",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    var s: S = mk(i);
    var k: i32 = 0;
    while (k < 3) { s = mk(i + k); k = k + 1; }
    var j: i32 = churn(i);
    if (s.name.len() != 31) { return 0 - 1; }
    return (s.name.len() + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 56, balance: true,
		},
		{
			// REFUSED: the old value is in a live container when the rebind
			// orphans it. Releasing there frees a box `keep` still points at, so
			// the readback is what proves the refusal is load-bearing.
			name: "alias_into_container",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    var s: S = mk(i);
    var keep: S[] = [];
    keep = keep.append(s);
    s = mk(i + 1);
    var j: i32 = churn(i);
    if (keep[0].name.len() != 31) { return 0 - 1; }
    return (s.name.len() + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 56,
		},
		{
			// REFUSED: a field is carried out of the box the rebind orphans, so
			// the deep drop would free a buffer `held` still reads.
			name: "field_moved_out",
			src: freshRetRebindDecl + `function round(i: i32): i32 {
    var s: S = mk(i);
    var held: string = s.name;
    s = mk(i + 1);
    var j: i32 = churn(i);
    if (held.len() != 31) { return 0 - 1; }
    return (s.name.len() + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 56,
		},
		{
			// REFUSED: the final value escapes, so body_unsafe_for declines the
			// whole chain — the frame owns neither end of it.
			name: "rebind_then_return",
			src: freshRetRebindDecl + `function build(i: i32): S {
    var s: S = mk(i);
    s = mk(i + 1);
    return s;
}
function round(i: i32): i32 {
    var p: S = build(i);
    var j: i32 = churn(i);
    if (p.name.len() != 31) { return 0 - 1; }
    return (p.name.len() + j) % 101;
}` + freshRetRebindCheckedMain,
			want: 56,
		},
	}
}

// TestSelfHostFreshRetRebindX86_64 — a fresh-ret-call struct local keeps its
// reclaim credit across a rebind, with the three escape shapes still refused.
func TestSelfHostFreshRetRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range freshRetRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "frrb_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a rebind released a box "+
					"it did not own; 100 = a held string read back short, i.e. the release "+
					"beat a live reader; 139 = it read freed memory)", tc.name, exit, tc.want)
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
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
			if !tc.balance && allocs == frees && live == 0 {
				t.Errorf("%s: %s — this shape ESCAPES and is deliberately refused the "+
					"credit; balancing means the gate that declines it stopped firing", tc.name, summary)
			}
		})
	}
}

// TestSelfHostFreshRetRebindSanitizeX86_64 — the same cases recompiled under
// FERN_SANITIZE, which is what reports an over-release rather than a leak. The
// census leg above cannot: a correct fix and a premature free both read
// allocs == frees.
func TestSelfHostFreshRetRebindSanitizeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range freshRetRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			progBin := buildBin(t, gcc, dir, "frrbsan_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s under FERN_SANITIZE exited %d, want %d "+
					"(134 = the quarantine caught a write to freed memory)", tc.name, exit, tc.want)
			}
		})
	}
}

// TestSelfHostFreshRetRebindWasmIR — the wasm sibling. Exit code only; the wasm
// leg carries no leak census.
func TestSelfHostFreshRetRebindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fresh-ret rebind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range freshRetRebindCases() {
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
			watFile := filepath.Join(dir, "frrb_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("fresh-ret rebind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostFreshRetRebindIRArm64 — the arm64 sibling under qemu.
func TestSelfHostFreshRetRebindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range freshRetRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "frrb_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
