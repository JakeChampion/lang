package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strArrFieldBufferReleaseCases pin a THREE-WAY emitter disagreement about a
// struct's `string[]` field, found while measuring the conformance case
// alloc_flat_fresh_array_arg: one shared lowering change moved it on the wasm
// leg and not at all on x86-64 or arm64.
//
// lower_expr's ExprStructLit array arm retains a BARE IDENT naming an rc-tracked
// slot, so `S { xs: p }` hands the field a counted reference; a fresh literal or
// a proven producer gives it one the field solely owns. Either way the box has
// something to give back. The register backends reached a `string[]` field only
// through their DEEP arm (__fern_str_arr_free, gated on the "strfldok:arr:<T>"
// strarrfld admission), so a type the scan refused — including one refused only
// on its READS — got no release at all and kept one buffer per construction for
// the program's life.
//
// The scan's verdict therefore splits. The READ half gates the ELEMENT walk,
// which is what can dangle an element alias. The STORE half is what the BUFFER
// dec needs, and it is emitted on its own as "strfldok:arrbuf:<T>": a store the
// scan refused (a field read, an `.append` on one) is an uncounted alias, and
// dec'ing that frees a buffer another owner still holds — which is what the
// per-module emit-all fixpoint said when this arm was first written ungated.
//
// The probes use SSO-short elements on purpose. A non-admitted field's element
// boxes still leak — that is the admission's job, not this one's — so wide
// elements would swamp the buffer in the measurement. With short ones the
// buffer is the only heap object per round, and each case returns the MEASURED
// bytes per round as its exit code: a regression reports its own size instead
// of a bare "not zero". x86-64 and arm64 both read 56 before the fix; wasm
// already read 0 and is here to pin that it stays there.
var strArrFieldBufferReleaseCases = []struct {
	name string
	src  string
	want int
}{
	// SCOPE EXIT (__struct_drop_<T>). `Node` routes field reclaim through its
	// `name: string` field, so the drop helper is emitted; `f.deps[0].len()` is
	// an element read, so "strfldok:arr:Node" is withheld and the deep arm is
	// not taken. The buffer still has to go back. 56 B/round before, 0 after.
	{"strarr-field-drop-buffer-flat", `struct Node { name: string, deps: string[], mtime: i32 }
function mk(): string[] { var o: string[] = []; o = o.append("aa"); o = o.append("bb"); o = o.append("cc"); return o; }
function node(name: string, deps: string[], mtime: i32): Node { return Node { name: name, deps: deps, mtime: mtime }; }
function round(pre: string, n: i32): i32 { var f: Node = node(pre, mk(), n); return f.deps.len() + f.deps[0].len() + f.name.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre, i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var w: i32 = churn(2000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(2000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 2000;
}`, 0},
	// LOOP REBIND (__field_reclaim_<T>). The same field, released by the OTHER
	// helper: `var r: Row = Row { … }` re-declared each iteration routes through
	// emit_field_reclaim_store, whose per-type body frees the superseded box's
	// replaced fields. Both helpers are emitted for both probes, so this row is
	// what separates them: with only the struct-drop arm fixed it still reads
	// 55, because the buffer that leaks here is the one every iteration but the
	// last supersedes.
	{"strarr-field-rebind-buffer-flat", `struct Row { tag: string, cells: string[] }
function mk(): string[] { var o: string[] = []; o = o.append("aa"); o = o.append("bb"); o = o.append("cc"); return o; }
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var r: Row = Row { tag: "t", cells: mk() };
        acc = (acc + r.cells.len() + r.cells[0].len()) % 251;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(2000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(2000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 2000;
}`, 0},
	// The ADMITTED path must not be displaced by the shallow arm: no element
	// read, so "strfldok:arr:Node" holds and __fern_str_arr_free deep-frees the
	// elements AND the buffer. Wide elements, bounded high-water — a shallow arm
	// that shadowed the deep one would strand three element boxes per round and
	// blow the 4 KB budget over the second 2000-iteration churn.
	{"strarr-field-admitted-deep-flat", `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var o: string[] = []; var i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function node(name: string, deps: string[], mtime: i32): Node { return Node { name: name, deps: deps, mtime: mtime }; }
function round(pre: string, n: i32): i32 { var f: Node = node(w(pre), mk(pre), n); return f.deps.len() + f.name.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre, i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var w0: i32 = churn(2000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(2000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (w0 != x) { return 97; }
    return 0;
}`, 0},
	// The shallow arm must stay SHALLOW on a non-admitted field: the elements
	// are read through the struct and through the live local, so freeing them
	// would corrupt both. Wide elements (they leak — sound), values exact,
	// over-release detector at zero.
	{"strarr-field-nonadmitted-elements-safe", `struct Doc { title: string, lines: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var o: string[] = []; var i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function doc(title: string, lines: string[]): Doc { return Doc { title: title, lines: lines }; }
function round(pre: string): i32 { var d: Doc = doc(w(pre), mk(pre)); var junk: string[] = mk(pre); if (junk.len() < 0) { return 0; } return d.lines.len() + d.lines[0].len() + d.lines[2].len() + d.title.len(); }
function main(): i32 { var pre: string = "ab"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 132) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrArrFieldBufferReleaseIRX86_64 drives the cases through the
// self-hosted x86-64 compiler (emit_ir_struct_drop_one / emit_ir_field_reclaim_one).
func TestSelfHostStrArrFieldBufferReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldBufferReleaseCases {
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
				t.Errorf("%s = %d, want %d (a small non-zero is the leaked bytes per round; 98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrFieldBufferReleaseIRArm64 is the arm64 leg. Both helper
// bodies are hand-transcribed per backend rather than shared, which is why this
// divergence existed at all — the register backends agreed with each other and
// not with wasm.
func TestSelfHostStrArrFieldBufferReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldBufferReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (a small non-zero is the leaked bytes per round; 98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrFieldBufferReleaseWasmIR is the wasm leg: it read 0 on every
// case before the register backends were changed, and is here so that stays true
// — the fix is the other two catching up to it, not a new behaviour to port.
func TestSelfHostStrArrFieldBufferReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string[]-field buffer-release wasm IR e2e")
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

	for _, tc := range strArrFieldBufferReleaseCases {
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
				t.Errorf("%s = %d, want %d (a small non-zero is the leaked bytes per round; 98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
