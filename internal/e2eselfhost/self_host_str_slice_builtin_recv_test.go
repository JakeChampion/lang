package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strSliceBuiltinRecvCases pin the release of a SLICE receiver at a string
// BUILTIN method — `base[4:base.len()].starts_with(..)`.
//
// lower_str_method already stashed and drained a fresh receiver, but its gate was
// is_fresh_str_temp, which refuses a slice (a slice aliases its source's bytes,
// which is the question that predicate answers). str_borrowing_method is the
// warrant that applies instead: every method in that set reads the receiver's
// bytes and returns a scalar or a freshly allocated string, never the receiver or
// a view of it. trim / replace / chars / lines / split sit outside it precisely
// because they can alias, and they keep the leak.
//
// The drain had a second bug on top of the gate. free_stashed_str_args emits
// __fern_str_free, which SKIPS an immortal rc by design — so even once a slice
// receiver was stashed, draining it through that helper released nothing on the
// backends where a slice is a zero-copy immortal view. The receiver drain now
// uses __fern_str_view_free, which frees the 24-byte box alone.
//
// Measured, two compilers from the same commit, 400 rounds:
//
//	method                x86-64        wasm
//	starts_with           9600 -> 0     48000 -> 0
//	ends_with             9600 -> 0     48000 -> 0
//	contains              9600 -> 0     48000 -> 0
//	index_of              9600 -> 0     48000 -> 0
//	to_ascii_upper        9600 -> 0     48000 -> 0
//
// The two columns are different objects: a 24-byte view box on the register
// backends, a payload-sized copy on wasm, where a slice is not zero-copy.
//
// NOT covered, and deliberately: `.len()`. It never reaches lower_str_method —
// is_str_builtin_method excludes it and it has its own receiver-release path —
// and on the register backends a slice there is a FRAME box that never touches
// the heap, so it already measures 0. On wasm it still strands 48000. Closing
// that means touching the path every `<expr>.len()` takes, where the register
// lowering currently allocates nothing; that is its own slice, not a rider here.
const sliceBuiltinPrelude = `import "std/i32";
import "std/i64";
import "std/string";
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
`

// sliceBuiltinHeap wraps a `round` body in the churn/heap-delta harness. 4096 is
// far under both measured leaks and far over the 0 a released box produces.
func sliceBuiltinHeap(body string) string {
	return sliceBuiltinPrelude + `function round(pre: string): i32 {
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

var strSliceBuiltinRecvCases = []struct {
	name string
	src  string
	want int
}{
	// A scalar predicate: reads the bytes, returns a boolean, keeps nothing.
	{"str-slice-recv-builtin-starts-with-flat", sliceBuiltinHeap(`    if (base[4:base.len()].starts_with("efgh")) { return 1; }
    return 0;`), 0},
	// index_of returns a position, and takes the same path contains does.
	{"str-slice-recv-builtin-index-of-flat", sliceBuiltinHeap(`    return base[4:base.len()].index_of("wide");`), 0},
	// A TRANSFORM rather than a predicate: to_ascii_upper allocates a new buffer,
	// so the receiver is dead on a different code path through the same stash.
	{"str-slice-recv-builtin-upper-flat", sliceBuiltinHeap(`    return base[4:base.len()].to_ascii_upper().len();`), 0},
	// REFUSED: trim returns a VIEW of its receiver, so releasing the receiver's box
	// frees what the result points into. It is outside str_borrowing_method and
	// stays outside; this case reads the result and the source afterwards.
	{"str-slice-recv-builtin-trim-refused", sliceBuiltinPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var t: str = base[4:base.len()].trim();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (t.index_of("XXXX") >= 0) { return 0 - 1; }
    if (!t.starts_with("efgh-a-wide")) { return 0 - 2; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 3; }
    return t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 3000) { var r: i32 = round(pre); if (r != 102) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// REFUSED: split's result carries views INTO the receiver's bytes, so the box
	// has to outlive the call. Also outside str_borrowing_method.
	{"str-slice-recv-builtin-split-refused", sliceBuiltinPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var parts: string[] = base[4:base.len()].split("-");
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (parts.len() < 2) { return 0 - 1; }
    if (parts[0] != "efgh") { return 0 - 2; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 3; }
    return parts.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 18) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// The SOURCE outlives the released box. Freeing a 24-byte view header must
	// leave the shared bytes alone — the immortal-rc case in __fern_str_view_free
	// is what makes that true, and a plain __fern_str_free here would have been
	// both wrong in kind and inert in practice.
	{"str-slice-recv-builtin-source-live", sliceBuiltinPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var hit: boolean = base[4:base.len()].starts_with("efgh");
    var up: string = base[4:base.len()].to_ascii_upper();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("ZZZZZZZZ");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (!hit) { return 0 - 1; }
    if (up.index_of("XXXX") >= 0) { return 0 - 2; }
    if (!up.starts_with("EFGH-A-WIDE")) { return 0 - 3; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 4; }
    return base.len() + up.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 3000) { var r: i32 = round(pre); if (r != 208) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrSliceBuiltinRecvIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostStrSliceBuiltinRecvIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strSliceBuiltinRecvCases {
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
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceBuiltinRecvIRArm64 is the arm64 leg.
func TestSelfHostStrSliceBuiltinRecvIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strSliceBuiltinRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceBuiltinRecvWasmIR is the wasm leg, where the released box
// is a payload-sized copy rather than a 24-byte header.
func TestSelfHostStrSliceBuiltinRecvWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping slice builtin-receiver wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strSliceBuiltinRecvCases {
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
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
