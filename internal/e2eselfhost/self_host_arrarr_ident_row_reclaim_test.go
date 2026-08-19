package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// arrarrIdentRowCases pin #6527's self-host half: a `string[][]` whose ROW is a
// bare local earned no deep reclaim at all.
//
// The rows-are-literals spelling was already flat — arrarr_free_helper_of picks
// __fern_strarrarr_free by kind — so the leak was never helper SELECTION, which
// is what the issue attributed it to. It was the freshness proof: both
// arrarr_lit_is_fresh and its strict string sibling required every row to be an
// array LITERAL, so `var outer: string[][] = [inner, [...]]` fell to a flat
// __fern_arr_dec per level and every element string was stranded (120 B/round
// for one local row, 192 with a literal row beside it).
//
// A row that names a local is admitted when this construction is the local's
// last use: declared earlier in the same statement list from an array literal,
// mentioned nowhere in between, once here, never after. That is the invariant a
// literal row has for free — its buffer is born in the slot — so the deep
// reclaim frees nothing anything else still reads.
//
// The refusal cases carry the weight. They must keep VALUES correct and the
// over-release counter at zero; they are deliberately not asserted flat,
// because a refused row still leaks exactly as it did before.
var arrarrIdentRowCases = []struct {
	name string
	src  string
	want int
	wasm bool
}{
	// The filed shape: a local row beside a literal row.
	{"ident-row-with-literal-row", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    if (k == 1) { return "one"; }
    return "many";
}
function nested(k: i32): i32 {
    var inner: string[] = ["alpha-payload-" + tag(k), "beta-payload-" + tag(k)];
    var outer: string[][] = [inner, ["gamma-payload-" + tag(k)]];
    return outer[0][0].len() + outer[1][0].len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + nested(i % 3)) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = (acc + nested(j % 3)) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// The local row alone — no literal row to carry the credit.
	{"ident-row-only", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    var inner: string[] = ["alpha-payload-" + tag(k), "beta-payload-" + tag(k)];
    var outer: string[][] = [inner];
    return outer[0][0].len() + outer[0][1].len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + nested(i % 2)) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = (acc + nested(j % 2)) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// The PRODUCER spelling: the same literal returned rather than bound. It
	// rides the "AAC:" registry, whose two freshness flags read the identical
	// pair of gates, so the widening has to reach it or the caller's binding
	// earns the credit in one spelling and not the other.
	{"producer-returns-ident-row", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function mk(k: i32): string[][] {
    var inner: string[] = ["alpha-payload-" + tag(k), "beta-payload-" + tag(k)];
    return [inner, ["gamma-payload-" + tag(k)]];
}
function consume(k: i32): i32 {
    var g: string[][] = mk(k);
    return g[0][0].len() + g[1][0].len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + consume(i % 2)) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = (acc + consume(j % 2)) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// REFUSED: the row local is read after the construction, so it is not this
	// statement's to consume. Both readings must still see live characters.
	{"row-read-after-is-refused", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    var inner: string[] = ["alpha-payload-" + tag(k)];
    var outer: string[][] = [inner];
    var a: i32 = outer[0][0].len();
    var b: i32 = inner[0].len();
    if (a != b) { return 0 - 1; }
    return a + b;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var v: i32 = nested(i % 2);
        if (v < 0) { return 97; }
        acc = (acc + v) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// REFUSED: the row's ELEMENT aliases a live string local read afterwards.
	// A string-kind deep reclaim frees element POINTERS, so crediting this
	// would dangle `s`.
	{"row-element-aliases-live-local", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    var s: string = "shared-payload-" + tag(k);
    var inner: string[] = [s];
    var outer: string[][] = [inner];
    var a: i32 = outer[0][0].len();
    var b: i32 = s.len();
    if (a != b) { return 0 - 1; }
    return a + b;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var v: i32 = nested(i % 2);
        if (v < 0) { return 97; }
        acc = (acc + v) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// REFUSED: one row local placed into TWO arr-of-arrs. Only one of them
	// could own it, and neither is the local's last use.
	{"row-placed-twice", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    var inner: string[] = ["twice-payload-" + tag(k)];
    var o1: string[][] = [inner];
    var o2: string[][] = [inner];
    var a: i32 = o1[0][0].len();
    var b: i32 = o2[0][0].len();
    if (a != b) { return 0 - 1; }
    return a + b;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var v: i32 = nested(i % 2);
        if (v < 0) { return 97; }
        acc = (acc + v) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// REFUSED: the local is grown between its declaration and the
	// construction, so the declaration's initializer no longer describes the
	// buffer that reaches the row. This is what the "mentioned nowhere in
	// between" clause is for — the elements proof is read off that
	// initializer, and here it would be read off the wrong one.
	{"row-mutated-between-decl-and-use", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    var inner: string[] = ["first-payload-" + tag(k)];
    inner = inner.append("second-payload-" + tag(k));
    var outer: string[][] = [inner];
    return outer[0][0].len() + outer[0][1].len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var v: i32 = nested(i % 2);
        if (v <= 0) { return 97; }
        acc = (acc + v) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},
}

// TestSelfHostArrArrIdentRowReclaimX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostArrArrIdentRowReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrarrIdentRowCases {
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
				t.Errorf("%s = %d, want %d (98 = element strings leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostArrArrIdentRowReclaimArm64 is the arm64 leg. The divergence this
// closes was identical on all three targets, which is what placed it in the
// shared reclaim path rather than in an emitter — so all three have to answer.
func TestSelfHostArrArrIdentRowReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrarrIdentRowCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = element strings leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostArrArrIdentRowReclaimWasmIR is the wasm leg.
func TestSelfHostArrArrIdentRowReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping arr-of-arr ident-row reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrarrIdentRowCases {
		if !tc.wasm {
			continue
		}
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
				t.Errorf("%s = %d, want %d (98 = element strings leaked; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
