package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strFldDropGateCases pin the STRFLDOK gating of the per-type struct-drop
// STRING arm (#3425): __struct_drop_<T>'s k_str free (and its arm64 / wasm
// siblings) must fire ONLY for types admitted by the whole-program
// escaping-read scan — the SAME "strfldok:<T>" verdict that gates
// __field_reclaim_<T>'s string arm and the construction-side retain
// (slit_reclaim → struct_routes_field_reclaim). Before the gate, the drop
// body freed string fields of NON-admitted types too, while construction
// never retained them: a string aliased into several such structs was an
// uncounted reference the first element drop freed out from under the rest —
// heap corruption. The shape below is the minimal form of what killed the
// IR-routed merged-bundle self-compile (ir.Op's shared .str freed by
// __struct_arr_elems_drop_ir__Op → __struct_drop_ir__Op): `esc` returns the
// field (an escaping read → Rec is NOT admitted), `shared` is aliased into
// every element, and Outer's exit drop walks the Rec[] field through the
// array-element deep-drop. Pre-gate this segfaulted (or ticked the
// underflow detector); with the gate the un-admitted type keeps the sound
// leak and the shared string survives intact.
var strFldDropGateCases = []struct {
	name string
	src  string
	want int
}{
	{"unadmitted-shared-string-survives-elem-drop", `struct Rec { tag: string, n: i32 }
struct Outer { rs: Rec[], m: i32 }
function esc(r: Rec): string { return r.tag; }
function go(shared: string): i32 {
    var rs: Rec[] = [];
    var i: i32 = 0;
    while (i < 3) { rs = rs.append(Rec { tag: shared, n: i }); i = i + 1; }
    var o: Outer = Outer { rs: rs, m: 7 };
    var t: string = esc(o.rs[0]);
    return o.m + t.len();
}
function main(): i32 {
    var shared: string = "ab" + "cd";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) { acc = (acc + go(shared)) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (shared.len() != 4) { return 98; }
    if (shared[0] != 97) { return 97; }
    return 0;
}`, 0},
	// ADMITTED control: no escaping read anywhere, so the type IS admitted,
	// construction retains the aliased field, and the drop's string free
	// stays balanced — fresh per-iteration strings reclaim (bounded churn)
	// with the detector at zero. Guards that the gate didn't turn off the
	// slice-3 reclaim for admitted types.
	{"admitted-string-field-still-reclaims", `struct Rec { tag: string, n: i32 }
struct Outer { rs: Rec[], m: i32 }
function go(pre: string): i32 {
    var rs: Rec[] = [];
    var i: i32 = 0;
    while (i < 3) { rs = rs.append(Rec { tag: pre + "x", n: i }); i = i + 1; }
    var o: Outer = Outer { rs: rs, m: 7 };
    return o.m + o.rs[0].n;
}
function churn(m: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var w: i32 = churn(2000);
    var x: i32 = churn(2000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostStrFldDropGateIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run) — value-correct + underflow-guarded.
func TestSelfHostStrFldDropGateIRX86_64(t *testing.T) {
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

	for _, tc := range strFldDropGateCases {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (99 = over-release/underflow; 98/97 = shared string freed out from under a live alias; 139 = heap corruption segfault)", tc.name, code, tc.want)
			}
		})
	}
}
