package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Record-form enum variants (`enum Shape { Rect { w: i32, h: i32 } }`) on the
// self-host IR path (#6676). The payload layout is the positional one — the
// variant record carries the same `__ev` / `__ev1` marker fields — so the field
// names are a parse concern only: parse_enum_decl records them alongside the
// markers and resolve_variant_fields_module rewrites `Rect { h, w }` into the
// positional `Rect(w, h)` before the checker or any backend sees it. Every case
// is checked against the native interpreter oracle, so a wrong ORDER (the
// failure mode the reorder exists to prevent) shows up as a wrong exit code.
//
// The first-arm `Ident {` shape is shared with the struct-pattern desugar; which
// one a match takes is decided on whether the head names a struct declared in
// the same file (parser.is_struct_pattern_arm), which is why several cases
// declare a struct alongside the enum.
var selfHostRecordVariantCases = []struct {
	name string
	src  string
}{
	{"decl_and_match", `enum Shape {
  Circle { r: i32 },
  Rect { w: i32, h: i32 },
  Unit,
  Pair(i32, i32),
}
function area(s: Shape): i32 {
  match (s) {
    Circle { r } => { return 3 * r * r; },
    Rect { w, h } => { return w * h; },
    Unit => { return 0; },
    Pair(a, b) => { return a + b; },
  }
  return 0 - 1;
}
function main(): i32 {
  return area(Rect(3, 4)) + area(Circle(2)) + area(Unit) + area(Pair(5, 6));
}`},
	// Field order in the pattern is free; the bindings must still land on the
	// payloads in DECLARATION order.
	{"reordered_fields", `enum Shape { Rect { w: i32, h: i32 } }
function f(s: Shape): i32 { match (s) { Rect { h, w } => { return w * 10 + h; }, } return 0; }
function main(): i32 { return f(Rect(3, 7)); }`},
	// Value position: the parser desugars the match into an immediately-invoked
	// closure, so the record pattern sits inside a lambda body.
	{"expr_form", `enum Shape { Circle { r: i32 }, Rect { w: i32, h: i32 } }
function area(s: Shape): i32 {
  return match (s) {
    Circle { r } => 3 * r * r,
    Rect { h, w } => w * h,
  };
}
function main(): i32 { return area(Rect(3, 4)) + area(Circle(2)); }`},
	// A record variant still matches positionally — the layout is the same.
	{"positional_match_of_record", `enum Shape { Rect { w: i32, h: i32 }, Unit }
function f(s: Shape): i32 { match (s) { Rect(w, h) => { return w - h; }, Unit => { return 0; } } return 0; }
function main(): i32 { return f(Rect(9, 4)) + f(Unit); }`},
	{"guarded_arms", `enum Shape { Rect { w: i32, h: i32 }, Unit }
function f(s: Shape): i32 {
  match (s) {
    Rect { w, h } when w > h => { return 1; },
    Rect { w, h } => { return 2; },
    Unit => { return 3; },
  }
  return 0;
}
function main(): i32 { return f(Rect(5, 2)) * 100 + f(Rect(1, 9)) * 10 + f(Unit); }`},
	{"string_payload", `enum Msg { Note { id: i32, text: string }, Empty }
function f(m: Msg): i32 { match (m) { Note { text, id } => { return text.len() + id; }, Empty => { return 0; } } return 0; }
function main(): i32 { return f(Note(5, "abcd")) + f(Empty); }`},
	// A struct pattern in the same file must keep taking the struct desugar.
	{"struct_pattern_alongside", `struct Point { x: i32, y: i32 }
enum Shape { Rect { w: i32, h: i32 } }
function ps(p: Point): i32 { match (p) { Point { x, y } => { return x + y; } } return 0; }
function sh(s: Shape): i32 { match (s) { Rect { h, w } => { return w * h; }, } return 0; }
function main(): i32 { return ps(Point { x: 1, y: 2 }) + sh(Rect(3, 4)); }`},
	// The enum is declared BELOW the match that destructures it: the head-name
	// scan runs over the whole token stream, not the decls parsed so far.
	{"enum_declared_after_use", `function f(s: Shape): i32 { match (s) { Rect { h, w } => { return w * 10 + h; }, } return 0; }
enum Shape { Rect { w: i32, h: i32 } }
function main(): i32 { return f(Rect(2, 5)); }`},
	{"at_binding", `enum Shape { Rect { w: i32, h: i32 }, Unit }
function f(s: Shape): i32 { match (s) { r @ Rect { h, w } => { return w * h; }, Unit => { return 0; } } return 0; }
function main(): i32 { return f(Rect(6, 7)) + f(Unit); }`},
}

func TestSelfHostRecordVariantX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostRecordVariantCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCapture(t, gcc, runner, driverBin, prog, "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostRecordVariantArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostRecordVariantCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCapture(t, x86gcc, x86runner, driverBin, prog, "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostRecordVariantWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host record-variant wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostRecordVariantCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("wasm IR driver failed: %v", err)
			}
			watFile := filepath.Join(dir, "recvar_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatal("wasmtime did not exit normally")
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("record-variant wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
