package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strArrFieldTransientReadCases pin the READ half of the strarrfld admission,
// the half that gates the DEEP element walk (__fern_str_arr_free at scope exit,
// $__fern_arr_dec_ptr on wasm).
//
// The scan tolerated exactly one borrow of a `string[]` field: `x.f.len()`, the
// whole-array length. An ELEMENT read marked the field unsafe however briefly
// the element lived, so `f.deps[0].len()` cost the type its admission and every
// element box leaked. That is a line irlower already knows how to draw the other
// way round: strarr_expr_unsafe, the same question asked about a string[] LOCAL,
// separates a transient element receiver from a lasting alias and admits the
// former. The field scan now draws it too.
//
// Transient means the element is borrowed for one call and never bound: `len`,
// the predicates (`starts_with` / `ends_with` / `contains` / `index_of`) and the
// fresh-copy transforms (`to_ascii_upper` / `to_ascii_lower` / `reverse` /
// `repeat`). `.trim()` and `.replace()` are deliberately absent — both can
// return a VIEW of the receiver's buffer, which outlives the call and is exactly
// the alias the deep free would dangle.
//
// This closes the AL-01 conformance case alloc_flat_fresh_array_arg on all three
// backends: 424 B/round to flat on x86-64 and arm64, and flat stays flat on
// wasm.
var strArrFieldTransientReadCases = []struct {
	name string
	src  string
	want int
}{
	// The shape the conformance case is built from: the caller reads
	// `f.deps[0].len()`, which is transient, so the field keeps its admission
	// and __struct_drop_Node's deep arm frees the three element boxes. 98
	// (leaked, ~200 B/round of element boxes) before; flat after.
	{"strarr-field-transient-elem-read-flat", `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var o: string[] = []; var i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function node(name: string, deps: string[], mtime: i32): Node { return Node { name: name, deps: deps, mtime: mtime }; }
function round(pre: string, n: i32): i32 { var f: Node = node(w(pre), mk(pre), n); return f.deps.len() + f.name.len() + f.deps[0].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre, i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var a: i32 = churn(2000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(2000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, 0},
	// A VIEW receiver is not transient: `.trim()` returns a zero-copy `str` over
	// the element's buffer, and `v` outlives the call. The field must stay
	// refused — the emitted __struct_drop_View has no element walk — or `v`
	// reads freed bytes. Value-exact against a freshly computed reference so a
	// recycled buffer of the same length cannot pass.
	{"strarr-field-view-elem-read-refused", `struct View { title: string, lines: string[] }
function w(pre: string): string { return pre + "  a-wide-element-past-the-inline-threshold  "; }
function mk(pre: string): string[] { var o: string[] = []; var i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function doc(title: string, lines: string[]): View { return View { title: title, lines: lines }; }
function expect(pre: string): i32 { var r: string[] = mk(pre); return r[0].trim().len() + w(pre).len(); }
function round(pre: string): i32 { var d: View = doc(w(pre), mk(pre)); var v: str = d.lines[0].trim(); var pad: string[] = mk(pre); if (pad.len() < 0) { return 0; } return v.len() + d.title.len(); }
function main(): i32 { var pre: string = "ab"; var e: i32 = expect(pre); var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != e) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// A BOUND element is the other lasting alias: `var t = d.lines[0]` hands the
	// box to a local that outlives the struct's drop. Still refused; `t` reads
	// live bytes under allocation pressure.
	{"strarr-field-bound-elem-read-refused", `struct Held { title: string, lines: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var o: string[] = []; var i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function doc(title: string, lines: string[]): Held { return Held { title: title, lines: lines }; }
function hold(pre: string): i32 { var d: Held = doc(w(pre), mk(pre)); var t: string = d.lines[0]; return t.len() + d.title.len(); }
function round(pre: string): i32 { var k: i32 = hold(pre); var pad: string[] = mk(pre); var pad2: string[] = mk(pre); if (pad.len() < 0 || pad2.len() < 0) { return 0; } return k; }
function main(): i32 { var pre: string = "ab"; var e: i32 = w("ab").len() * 2; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != e) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrArrFieldTransientReadIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostStrArrFieldTransientReadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldTransientReadCases {
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
				t.Errorf("%s = %d, want %d (98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrFieldTransientReadIRArm64 is the arm64 leg: the admission is
// shared irlower, the element walk is a per-backend transcription.
func TestSelfHostStrArrFieldTransientReadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldTransientReadCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrFieldTransientReadWasmIR is the wasm leg, where the deep
// release is $__fern_arr_dec_ptr.
func TestSelfHostStrArrFieldTransientReadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string[]-field transient-read wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strArrFieldTransientReadCases {
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
				t.Errorf("%s = %d, want %d (98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
