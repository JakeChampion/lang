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

// `var t: string = base.trim()` leaked its box on every backend.
//
// trim is listed in `str_local_binding_is_fresh`'s DELIBERATELY EXCLUDED set as
// a "zero-copy VIEW into the receiver buffer", alongside the genuine
// receiver-identity cases. That reasoning conflates the BOX with the DATA.
// `asmcore.rt_src_str_trim` returns `s[start:end]` — always a slice, never the
// receiver — so the box is new on every call and nobody else names it. Only the
// bytes belong to the receiver.
//
// The measurement settles it without reading any of that: 24 bytes per round on
// the register backends is exactly one view box, and a result that WERE the
// receiver would add no box at all.
//
// Measured, 400 rounds of the harness below, a pair of compilers from the same
// commit:
//
//	shape                      x86-64          arm64            wasm
//	base.trim()             9600 -> 0       9600 -> 0      48000 -> 0
//	base.trim().trim()     19200 -> 0      19200 -> 0      96000 -> 0
//
// The two backends need different releases and get them from one flag. On the
// register backends the box carries the immortal rc the slice op stamps, so
// `__fern_str_view_free` returns the 24 bytes and leaves the data alone; on wasm
// the slice COPIES, so the same helper takes `__fern_str_free`'s path and frees
// box and data together. `LocalInfo.str_view_local`, set at the binding site
// where the receiver's type is known, is what makes the release site choose it —
// the credit itself is an ordinary `STR:` name, because "is this box mine" is the
// same question every other entry in that set answers.
//
// The receiver-type test is in `trim_str_init`, reading declared types, for the
// same reason `join_strarr_init` and `tostr_scalar_init` do: a user-declared
// `.trim()` on another type would otherwise be admitted by name alone, and its
// result may alias a field the receiver still owns. That shape is the last case
// below.

const strTrimPrelude = `function w(pre: string): string { return pre + "   -a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789   "; }
`

func strTrimHeap(body string, limit int) string {
	return strTrimPrelude + `function round(pre: string): i32 {
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

var strTrimHeapCases = []struct {
	name string
	body string
}{
	{"str-trim-view-released", `    var t: string = base.trim();
    return t.len() % 251;`},
	// A trim OF a trim: a view over a view on the register backends, so the
	// second box's data pointer is inside the first box's source. Both are
	// released, and neither release may touch the bytes.
	{"str-trim-of-trim-released", `    var t: string = base.trim();
    var u: string = t.trim();
    return (t.len() + u.len()) % 251;`},
}

var strTrimFaultCases = []struct {
	name string
	src  string
}{
	// LIVENESS: the receiver AND both trim results are read after decoy
	// allocations that would be handed a freed block if a release landed early
	// or freed the wrong thing. `base.len() == t.len() + 3` pins that the bytes
	// behind the view are intact, not merely readable.
	{"str-trim-source-and-result-live", strTrimPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var t: string = base.trim();
    var u: string = t.trim();
    var n: i32 = t.len();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!base.starts_with("abcdefgh   -")) { return 0 - 1; }
    if (!t.starts_with("abcdefgh   -")) { return 0 - 2; }
    if (!u.starts_with("abcdefgh   -")) { return 0 - 3; }
    if (t.index_of("XXXX") >= 0) { return 0 - 4; }
    if (base.index_of("YYYY") >= 0) { return 0 - 5; }
    if (u.len() != n) { return 0 - 6; }
    if (base.len() != n + 3) { return 0 - 7; }
    return 3;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 3) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// ESCAPE: the trim result is returned, so the credit is withheld and the
	// caller's read has to find both the box and the bytes.
	{"str-trim-escapes-return", strTrimPrelude + `function trimmed(pre: string): string { var base: string = w(pre); var t: string = base.trim(); return t; }
function round(pre: string): i32 {
    var t: string = trimmed(pre);
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!t.starts_with("abcdefgh   -")) { return 0 - 1; }
    return t.len() % 251;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; var want: i32 = round(pre); while (i < 2000) { if (round(pre) != want) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// A USER `.trim()` whose result ALIASES a field the receiver still owns.
	// Nothing may credit it — this is what proves trim_str_init's receiver-type
	// test carries its weight, since the heap cases above move either way.
	{"str-trim-user-method-not-credited", strTrimPrelude + `struct Holder { name: string, tag: string }
function (h: Holder) trim(): string { return h.name; }
function trimmed(h: Holder): i32 { var t: string = h.trim(); return t.len() % 251; }
function churn(pre: string): i32 { var a: string = w(pre + "1"); var b: string = w(pre + "2"); return a.len() + b.len(); }
function main(): i32 {
    var keep: Holder = Holder { name: w("aaaa"), tag: w("bbbb") };
    var i: i32 = 0;
    while (i < 2000) {
        if (trimmed(keep) < 0) { return 96; }
        if (churn("QQQQQQQQ") < 0) { return 95; }
        if (!keep.name.starts_with("aaaa   -")) { return 97; }
        if (!keep.tag.starts_with("bbbb   -")) { return 97; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`},
}

const strTrimExitHint = "98 = the view box was stranded; 99 = over-release; 97 = value corrupted; 96/95 = the probe's own guards"

func strTrimSources() []struct{ name, src string } {
	var out []struct{ name, src string }
	for _, tc := range strTrimHeapCases {
		out = append(out, struct{ name, src string }{tc.name, strTrimHeap(tc.body, 4096)})
	}
	for _, tc := range strTrimFaultCases {
		out = append(out, struct{ name, src string }{tc.name, tc.src})
	}
	return out
}

// TestSelfHostStrTrimViewReleaseIRX86_64 is the x86-64 leg.
func TestSelfHostStrTrimViewReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strTrimSources() {
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
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strTrimExitHint)
			}
		})
	}
}

// TestSelfHostStrTrimViewReleaseIRArm64 is the arm64 leg — the other backend
// whose trim yields a view rather than a copy.
func TestSelfHostStrTrimViewReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strTrimSources() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strTrimExitHint)
			}
		})
	}
}

// TestSelfHostStrTrimViewReleaseWasmIR is the wasm leg, where the same flag
// selects the copy-freeing path instead.
func TestSelfHostStrTrimViewReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping trim view release wasm IR e2e")
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

	for _, tc := range strTrimSources() {
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
				t.Errorf("%s = %d, want 0 (%s)", tc.name, got, strTrimExitHint)
			}
		})
	}
}
