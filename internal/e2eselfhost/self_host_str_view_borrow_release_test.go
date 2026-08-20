package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strViewBorrowReleaseCases pin the release of a SLICE TEMP standing at a string
// BORROW POSITION — `base[a:b].len()`, `base[a:b] == x`, `base[a:b] + y`, and the
// rest of the set lower_view_borrowed serves.
//
// The two backends reach "this temp costs nothing" by different routes, and only
// one of them existed. On the register backends view_frame_temp_ok routes the
// slice to lower_str_slice_frame, whose 24-byte box lives in three reserved
// frame slots and never reaches the heap. That predicate opens with
// `if (s.for_wasm()) { return false; }` — correctly, because wasm has no such
// storage: op_str_slice_frame's own comment says it "ignores it, its str_slice
// copies into a fresh inline block". So on wasm every borrow position built a
// payload-sized heap COPY and nothing released it.
//
// Measured, two compilers from the same commit, 400 rounds:
//
//	position              x86-64      wasm
//	.len() receiver         0      48000 -> 0
//	== comparison           0      48000 -> 0
//	concat operand          0      48000 -> 0
//	index base              0      48000 -> 0
//	slice source            0      54400 -> 0
//	len(x) builtin          0      48000 -> 0
//
// The register columns are 0 on both sides, so these gates only bite on the wasm
// leg. They are still run on all three: a future change that made the register
// path allocate would show up here, and the cost of the extra legs is a second.
//
// Blast radius is checked rather than asserted: the compiler's OWN x86-64
// emission is byte-identical across both compilers — 1.7M lines — because a
// parked slot is `0 - 1` on every non-wasm target and free_parked_view_after
// then emits nothing.
//
// One honest limit on the ORDERING. The drain lands after the consuming op, which
// is the only correct place — the op still has to read the box. But a build that
// frees BEFORE the op passes every probe here, because the free and the use are
// adjacent with no allocation able to intervene, so the read finds stale-but-
// intact memory. The placement is correct by construction and CONTRACT-ONLY by
// measurement; what the byte gates below witness is that the release happens at
// all, not where.
const viewBorrowPrelude = `import "std/i32";
import "std/i64";
import "std/string";
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function own2(s: string): string { return s + ""; }
`

// viewBorrowHeap wraps a `round` body in the churn/heap-delta harness. 4096 is
// far under the 48000 each position stranded and far over the 0 a released copy
// produces.
func viewBorrowHeap(body string) string {
	return viewBorrowPrelude + `function round(pre: string): i32 {
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
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`
}

var strViewBorrowReleaseCases = []struct {
	name string
	src  string
	want int
}{
	{"str-view-borrow-len-receiver", viewBorrowHeap(`    return base[4:base.len()].len();`), 0},
	{"str-view-borrow-comparison", viewBorrowHeap(`    if (base[4:base.len()] == "nope") { return 1; }
    return 0;`), 0},
	{"str-view-borrow-concat-operand", viewBorrowHeap(`    var t: string = base[4:base.len()] + "-tail";
    return t.len();`), 0},
	{"str-view-borrow-index-base", viewBorrowHeap(`    return (base[4:base.len()][0] as i32);`), 0},
	// A slice OF a slice: the inner one is the outer op's operand. On wasm the
	// outer str_slice copies, so the inner copy is dead the moment it has been
	// read — which is why this is a borrow position and not an alias.
	{"str-view-borrow-slice-source", viewBorrowHeap(`    return base[4:base.len()][1:5].len();`), 0},
	// The free spelling of `.len()`, which lowers through the same borrow path.
	{"str-view-borrow-free-len-builtin", viewBorrowHeap(`    return len(base[4:base.len()]);`), 0},
	// LIVENESS across every position at once, with the SOURCE and each RESULT
	// read afterwards behind decoy allocations that would be handed the freed
	// block if a release had landed too early.
	{"str-view-borrow-all-positions-live", viewBorrowPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var n: i32 = base[4:base.len()].len();
    var eq: boolean = base[4:base.len()] == "nope";
    var lt: boolean = base[4:base.len()] < "zzzz";
    var cat: string = base[4:base.len()] + "-tail";
    var ch: i32 = (base[4:base.len()][0] as i32);
    var sub: string = own2(base[4:base.len()][1:5]);
    var fl: i32 = len(base[4:base.len()]);
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("ZZZZZZZZ");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    if (base.index_of("XXXX") >= 0) { return 0 - 2; }
    if (n != 102 || fl != 102) { return 0 - 3; }
    if (eq) { return 0 - 4; }
    if (!lt) { return 0 - 5; }
    if (!cat.starts_with("efgh-a-wide")) { return 0 - 6; }
    if (!cat.ends_with("-tail")) { return 0 - 7; }
    if (ch != 101) { return 0 - 8; }
    if (sub != "fgh-") { return 0 - 9; }
    return n;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 3000) { var r: i32 = round(pre); if (r != 102) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrViewBorrowReleaseIRX86_64 is the x86-64 leg. Every case here
// already measured 0 before the change — the frame form is what makes that true —
// so this leg guards the register path against a regression rather than pinning
// a fix.
func TestSelfHostStrViewBorrowReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strViewBorrowReleaseCases {
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
				t.Errorf("%s = %d, want %d (98 = the slice copy was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrViewBorrowReleaseIRArm64 is the arm64 leg, the register path's
// twin.
func TestSelfHostStrViewBorrowReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strViewBorrowReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the slice copy was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrViewBorrowReleaseWasmIR is the leg this change is for: every
// case fails with 98 on the parent, where the slice copy is stranded.
func TestSelfHostStrViewBorrowReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping view-borrow release wasm IR e2e")
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

	for _, tc := range strViewBorrowReleaseCases {
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
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (98 = the slice copy was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
