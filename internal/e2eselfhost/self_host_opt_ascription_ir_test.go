package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// optAscriptionCases pin the Option/Result recovery for a CAST scrutinee
// (#5646 option 3).
//
// The parser desugars both casts to a unary op, with disjoint tags:
//
//	e as? T          -> as?_T          the dyn-Trait downcast, yielding Option[T]
//	e as Option[T]   -> as_Option[T]   a plain ascription; the tag IS the type
//
// Only the downcast was enumerated, so `match (None as Option[i32])` resolved to
// "" and bailed the enclosing function to the AST emitter. Its sibling
// ascription shapes — `var x = None as Option[i32]`, `return None as
// Option[i32]` — already lowered, because they bind or return through an
// annotation that carries the payload type. The value-position match is the one
// place the cast is the only written type, which is why it was the lone bail
// left in `TestSelfHostAsmIRPath` under FERN_STRICT_IR.
//
// `downcast-guard` is the `as?` shape, which shares the new helper: it must keep
// resolving, and it is the case a naive prefix test would break, since a tag is
// matched against "as?_" and "as_" in that order.
//
// Every `want` stays in [0, 126) — the wasm leg exits through WASI, which
// rejects anything above that.
var optAscriptionCases = []struct {
	name string
	src  string
	want int
}{
	{"asc-match-none", `
function main(): i32 { return match (None as Option[i32]) { Some(v) => v, None => 7 }; }
`, 7},
	{"asc-match-some", `
function main(): i32 { return match (Some(5) as Option[i32]) { Some(v) => v, None => 7 }; }
`, 5},
	{"asc-match-result-ok", `
function main(): i32 { return match (Ok(6) as Result[i32, string]) { Ok(v) => v, Err(e) => e.len() }; }
`, 6},
	{"asc-try", `
function f(x: i32): Option[i32] {
    var v: i32 = (Some(x) as Option[i32])?;
    return Some(v + 1);
}
function main(): i32 { return match (f(7)) { Some(v) => v, None => 1 }; }
`, 8},
	{"downcast-guard", `
trait P { function get(self: Self): i32; }
struct B { v: i32 }
impl P for B { function get(self: Self): i32 { return self.v; } }
function main(): i32 {
    var d: dyn P = B { v: 9 };
    match (d as? B) { Some(b) => { return b.v; }, None => { return 1; } }
}
`, 9},
}

// TestSelfHostOptAscriptionIRX86_64 asserts each cast scrutinee lowers on the IR
// path — proven by running under FERN_STRICT_IR, where a bail is exit 3 — and
// still produces the interpreter's answer.
func TestSelfHostOptAscriptionIRX86_64(t *testing.T) {
	gcc, runner, driverBin := strictIRDriver(t)
	dir := filepath.Dir(driverBin)

	for _, tc := range optAscriptionCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true)
			if code != 0 || len(asm) == 0 {
				t.Fatalf("%s did not lower on the IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			progBin := buildBin(t, gcc, dir, "asc_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostOptAscriptionIRWasm runs the same cases through the wasm IR
// backend, which shares the resolver.
func TestSelfHostOptAscriptionIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host opt-ascription wasm IR e2e")
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

	for _, tc := range optAscriptionCases {
		t.Run(tc.name, func(t *testing.T) {
			wat, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true, "-ir")
			if code != 0 || len(wat) == 0 {
				t.Fatalf("%s did not lower on the wasm IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			watFile := filepath.Join(dir, "opt_ascription_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := run.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("opt-ascription wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
