package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fstringIRCases pin f-string interpolation on the self-host IR path. An
// f-string `f"...{e}..."` desugars (in parser.fern) to the literal parts as
// string literals and each interpolant as `(e).to_string()`, folded left to
// right with `+`. The single-program driver resolves no imports and special-
// cases `.to_string()` on the two primitive receivers the IR path supports —
// a string (identity: `"x".to_string() == "x"`) and an i32 (routed to the
// `__fern_i32_to_string` runtime helper) — so importless f-strings that
// interpolate i32 + string values lower entirely through the IR path (no AST
// fallback). This holds for COMPUTED i32 interpolants too (`${x.len()}`,
// `${a + b}`), not just bare locals: the interpolant is re-parsed into a full
// expression and its i32 result routes through the same `to_string` fast-path.
// Each case builds an f-string and returns a small deterministic
// int (a `.len()`, a byte value, or an equality flag; all <= 126), pinned to
// the `"ir"` path; expectations were verified against the native interpreter
// (with `import "std/i32";` so `to_string` resolves there). FEATURE-AUDIT
// f-strings / interpolation row. f64 / i64 / bool receivers are NOT covered:
// their `to_string` needs an imported stdlib method, so those f-strings fall
// off the importless IR surface — extend here once the IR path grows native
// `to_string` lowering for them.
var fstringIRCases = []struct {
	name string
	main string
	want int
}{
	// single i32 interpolation + a literal prefix: "v=42" (len 4), also checked
	// for string equality against the literal "v=42".
	{"i32-eq-len", `var n: i32 = 42; var s: string = f"v={n}"; if (s == "v=42") { return s.len(); } return 99;`, 4},
	// literal + single interpolation: "x=7" (len 3).
	{"literal-interp", `var x: i32 = 7; var s: string = f"x={x}"; return s.len();`, 3},
	// two interpolants with a literal between: "1-2" (len 3).
	{"multi-interp", `var a: i32 = 1; var b: i32 = 2; var s: string = f"{a}-{b}"; return s.len();`, 3},
	// string-valued interpolant (identity to_string) inside literals: "[hi]" (len 4).
	{"string-interp", `var name: string = "hi"; var s: string = f"[{name}]"; return s.len();`, 4},
	// byte content: "n5"[1] == '5' == 53 — proves the interpolant text lands at
	// the right offset, not just that the length is right.
	{"byte-index", `var n: i32 = 5; var s: string = f"n{n}"; return s[1] as i32;`, 53},
	// multi-digit interpolation between literals: "=100=" (len 5).
	{"multi-digit", `var n: i32 = 100; var s: string = f"={n}="; return s.len();`, 5},
	// empty f-string desugars to "" (len 0).
	{"empty", `var s: string = f""; return s.len();`, 0},
	// COMPUTED interpolants (not a bare local) — the interpolant is re-parsed by
	// parser.parse_expr_from_text into a full expression, so `${e}` desugars to
	// `(e).to_string()` for any i32-valued `e`. These pin that a method-call result
	// and an arithmetic expression interpolate through the same i32 `to_string`
	// fast-path the bare-local cases use (`expr_recv_prim_type` classifies the call
	// / arith result as i32). The most common real f-string shape — `${x.len()}`.
	// i32 method-call result: "L4" (len 2).
	{"method-len", `var s: string = "abcd"; var t: string = f"L{s.len()}"; return t.len();`, 2},
	// i32 method-call result, byte-checked: "3"[0] == '3' == 51 — proves the
	// interpolated digit text is correct, not just the length.
	{"method-byte", `var s: string = "abc"; var t: string = f"{s.len()}"; return t[0] as i32;`, 51},
	// arithmetic interpolant: "=25=" (len 4).
	{"arith", `var n: i32 = 20; var s: string = f"={n + 5}="; return s.len();`, 4},
	// nested arithmetic, byte-checked: "9"[0] == '9' == 57.
	{"arith-byte", `var a: i32 = 4; var b: i32 = 5; var s: string = f"{a + b}"; return s[0] as i32;`, 57},
}

func fstringIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostFStringIRX86_64 routes each f-string case through the self-hosted
// x86-64 IR driver, pinned to the "ir" path.
func TestSelfHostFStringIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range fstringIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(fstringIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostFStringIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFStringIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f-string wasm IR e2e")
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

	for _, tc := range fstringIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(fstringIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "fstring_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("f-string wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
