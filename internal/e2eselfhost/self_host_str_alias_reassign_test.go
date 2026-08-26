package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A string local reassigned from an alias reclaims nothing ----------------
//
// `var s: string = "ab" + "cd"; var keep: string = "zz"; keep = s;` freed neither
// box — 40 allocs / 0 frees over 20 rounds. The BIND form (`var keep: string = s;`)
// has been at parity since #7282, so this is the same REASSIGN-vs-BIND split the
// struct limb had, in the class that has its own reclaim machinery.
//
// Two halves refused it, and they are NOT the ones the struct limb lifted:
//
//   - The TARGET is dropped by a BLANKET `index_of_str(reassigned, name) < 0` in
//     both STR collectors — any reassigned name, alias or not.
//   - The SOURCE is refused as a bare-ident escape, because
//     stmt_unsafe_for_alias_vb consults alias_ok in its StmtVar arm and NOT in its
//     StmtAssign arm. That asymmetry is the bug: a bind alias is forgiven, the
//     identical reassign alias is not.
//
// The blanket is safe to narrow because the other reassigned-string shape never
// depended on it: the accumulator (`s = s + part`) is credited by its own
// collector and lowered by emit_str_reclaim_store, which deliberately emits no inc
// — a consume-rebind replaces the slot's value with a fresh box rather than
// sharing one. It is pinned below as a control.
//
// Strings need no NODEEP:/SINKSHARE: distinction — a string is a single box with
// no field or element walk, so the alias takes the same "STR:" class. That is what
// makes this limb simpler than the struct one rather than merely different.
//
// The retain is emitted where every return path passes, right after the RHS is
// lowered — NOT via emit_arr_store's alias_inc, which emit_str_reclaim_store
// returns before ever reaching. Getting that wrong grants the credit without the
// retain, whose signature is exact count parity with exit 99.
//
// Every want below was confirmed against the native x86-64 backend. Exit 99 is
// reserved for __rc_underflow_count().

type strAliasReassignCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const strarMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func strAliasReassignCases() []strAliasReassignCase {
	return []strAliasReassignCase{
		{
			// THE REPRO. Base: 40 allocs / 0 frees.
			name: "string_alias_reassign",
			src: `function round(i: i32): i32 {
    var s: string = "ab" + "cd";
    var keep: string = "zz";
    keep = s;
    return keep.len() * 10 + (keep[0] as i32);
}
` + strarMain,
			want: 24, allocs: 40, frees: 40,
		},
		{
			// The same shape read back as a VALUE with fresh strings allocated after
			// the reassign, so a box freed too early would be reused before the
			// read. Counts alone read 120/120 whether the release is correct or an
			// over-release; this row and the underflow guard are what separate them.
			name: "reassign_read_back_after_churn",
			src: `function round(i: i32): i32 {
    var s: string = "ab" + "cd";
    var keep: string = "zz";
    keep = s;
    var j1: string = "pp" + "qq";
    var j2: string = "rr" + "ss";
    return keep.len() * 10 + (keep[0] as i32) + j1.len() - j1.len() + j2.len() - j2.len();
}
` + strarMain,
			want: 24, allocs: 120, frees: 120,
		},
		{
			// The BIND form, at parity since #7282. The control that says the
			// difference was the reassign and not the alias.
			name: "string_alias_bind_unchanged",
			src: `function round(i: i32): i32 {
    var s: string = "ab" + "cd";
    var keep: string = s;
    return keep.len() * 10 + (keep[0] as i32);
}
` + strarMain,
			want: 24, allocs: 40, frees: 40,
		},
		{
			// THE CONTROL THAT MUST NOT MOVE. The string-builder consume-rebind
			// routes through emit_str_reclaim_store, whose RHS is a FRESH box and
			// which emits no inc on purpose. The new collector must not claim it:
			// a retain here would leak, since nothing shares the box.
			name: "string_accumulator_unchanged",
			src: `function round(i: i32): i32 {
    var s: string = "";
    var k: i32 = 0;
    while (k < 4) { s = s + "ab"; k = k + 1; }
    return s.len();
}
` + strarMain,
			want: 63, allocs: 160, frees: 160,
		},
		{
			// A reassign whose RHS is a FRESH producer rather than an alias. It is
			// the accumulator's shape without the self-reference, and it must keep
			// taking the consume-rebind path rather than the new one.
			name: "fresh_rhs_reassign_unchanged",
			src: `function round(i: i32): i32 {
    var s: string = "ab" + "cd";
    s = "ef" + "gh";
    return s.len() * 10 + (s[0] as i32);
}
` + strarMain,
			want: 7, allocs: 80, frees: 80,
		},
		{
			// BORROWED PARAMS, and the gap this row pinned is now closed. `var q:
			// string = p; q = o;` where p and o are both params used to leave the
			// CALLER refusing its own release — 80/0, a sound leak — because
			// aliasing a param marked that param non-borrowable. The param verdict
			// now takes the same alias forgiveness the local credit does, so both
			// callers' strings are reclaimed.
			//
			// wantFrees moved 0 -> 80 and the row still guards the same thing, from
			// the other side: releasing a borrowed param's value is a use-after-free
			// only the CALLER can see, so it shows here as frees running AHEAD of
			// what the caller owns, not as a leak.
			//
			// Checked rather than assumed, because 0 -> 80 is also the direction an
			// over-release moves in. The answer is unchanged at 63,
			// __rc_underflow_count() is 0, -sanitize reports neither a
			// use-after-free nor a double free, and the settling form has `round`
			// read BOTH a and b back after the call with three fresh strings
			// allocated in between: 400/320 -> 400/400, answer 17 either way and
			// matching native. Native's counts are no oracle for this shape — it
			// const-folds and SSOs these literals to 0 allocs here, and on the
			// churn form it leaks 40 boxes of its own where the self-host is clean.
			name: "borrowed_param_reassign_borrowed",
			src: `function consume(p: string, o: string): i32 {
    var q: string = p;
    q = o;
    return q.len() + p.len();
}
function round(i: i32): i32 {
    var a: string = "ab" + "cd";
    var b: string = "ef" + "gh";
    return consume(a, b) + a.len() - a.len();
}
` + strarMain,
			want: 63, allocs: 80, frees: 80,
		},
	}
}

// TestSelfHostStrAliasReassignX86_64 is the leak-accounting leg.
func TestSelfHostStrAliasReassignX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strAliasReassignCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strar_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the credit was "+
					"granted without the retain that pays for it)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d. FEWER means the STRALIASSRC: "+
					"forgiveness stopped applying; MORE on the accumulator row means "+
					"the collector claimed a shape whose reassign carries no retain",
					tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostStrAliasReassignWasmIR — exit codes only, so what this leg catches
// is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostStrAliasReassignWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string alias-reassign wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strAliasReassignCases() {
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
			watFile := filepath.Join(dir, "strar_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("string alias-reassign wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStrAliasReassignIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStrAliasReassignIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strAliasReassignCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "strar_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
