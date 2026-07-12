package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f64MethodASTCases exercise std/float's f64 RECEIVER methods (`x.sqrt()`,
// `.floor()`, `.pow(y)`, …) driven through the self-host x86-64 native-binary
// track's **legacy AST emitter** (asm.fern), the path #4361 was filed against.
//
// #4361 recorded these as an asm.fern gap — the emitter was said to emit
// `call __fn_f64__sqrt` etc. without emitting the method bodies (an undefined
// reference at link). That gap has since closed: primitive-receiver method
// bodies (`(x: f64) sqrt() { return __sqrt_f64(x); }`) now emit on the AST
// path, so `x.sqrt()` resolves and runs. Nothing pinned it, though — the
// float-intrinsic suites cover the `__*_f64` FREE functions and the f64-recv
// IR suite covers USER methods on the IR path, but std/float's method forms on
// the AST path were unguarded. This is that guard.
//
// The AST path is reached by importing std/test unpruned (`-no-treeshake`):
// the merged module then exceeds asm_ir's 512-function IR-eligibility budget
// and routes to the AST emitter. Each case ASSERTS the route is "ast" (via
// -decide) so a future stdlib change that shifts it back to IR fails loudly
// here instead of silently stopping exercising the AST path. Results are
// oracle-checked against the interpreter.
var f64MethodASTCases = []struct {
	name string
	body string
}{
	{"sqrt", `var x: f64 = 16.0; return x.sqrt() as i32;`},    // 4
	{"floor", `var x: f64 = 7.9; return x.floor() as i32;`},   // 7
	{"ceil", `var x: f64 = 7.1; return x.ceil() as i32;`},     // 8
	{"trunc", `var x: f64 = 7.9; return x.trunc() as i32;`},   // 7
	{"round", `var x: f64 = 2.5; return x.round() as i32;`},   // 3
	{"abs", `var x: f64 = 0.0 - 5.5; return x.abs() as i32;`}, // 5
	{"pow", `var x: f64 = 2.0; return x.pow(5.0) as i32;`},    // 32
	{"exp", `var x: f64 = 2.0; return x.exp() as i32;`},       // 7
	{"log", `var x: f64 = 10.0; return x.log() as i32;`},      // 2
}

// f64MethodASTSrc builds a program that pulls std/test in unpruned so the
// merged module routes through the AST emitter, and calls an f64 method.
func f64MethodASTSrc(body string) string {
	return "import \"std/float\";\nimport \"std/test\";\nfunction main(): i32 { " + body + " }\n"
}

// TestSelfHostF64MethodAST_X86_64 pins std/float's f64 receiver methods
// compiling + linking + running on the self-host x86-64 AST (native-binary)
// path (#4361).
func TestSelfHostF64MethodAST_X86_64(t *testing.T) {
	gcc, _ := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "treeshake.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range f64MethodASTCases {
		t.Run(tc.name, func(t *testing.T) {
			src := f64MethodASTSrc(tc.body)
			want := interpExit(t, interpBin, src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			// The whole point is the AST emitter: assert the merged module
			// routes there, else the case stops exercising #4361's path.
			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-no-treeshake", "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ast" {
				t.Fatalf("%s routed %q, want \"ast\" (import set no longer busts the IR budget — update it)", tc.name, got)
			}

			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot, "-no-treeshake").Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "f64m_"+tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (AST path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
