package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f64MethodCases exercise std/float's f64 RECEIVER methods (`x.sqrt()`,
// `.floor()`, `.pow(y)`, …) through the self-host x86-64 native-binary track.
//
// This used to drive them through the legacy AST emitter (asm.fern) — the path
// #4361 was filed against — by importing std/test unpruned so the merged module
// busted asm_ir's 512-function IR budget and fell back. #3457 slice 5 deleted that
// emitter, so the mechanism is gone; the SUBJECT is not. The cases now import only
// std/float, route "ir", and still oracle-check every result against the
// interpreter, so what #4361 was actually about — std/float's method forms
// resolving and running on the self-host — stays pinned.
//
// #4361 recorded these as an asm.fern gap — the emitter was said to emit
// `call __fn_f64__sqrt` etc. without emitting the method bodies (an undefined
// reference at link). That gap has since closed: primitive-receiver method
// bodies (`(x: f64) sqrt() { return __sqrt_f64(x); }`) emit correctly, so
// `x.sqrt()` resolves and runs. Nothing pinned it, though — the float-intrinsic
// suites cover the `__*_f64` FREE functions and the f64-recv IR suite covers USER
// methods, but std/float's own method forms were unguarded. This is that guard.
//
// Each case ASSERTS the route is "ir" (via -decide), so a stdlib change that
// pushes this import set back over the budget fails loudly here rather than
// silently compiling through a path that no longer exists.
var f64MethodCases = []struct {
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

// f64MethodSrc builds a minimal program calling an f64 method. std/test is NOT
// imported any more: it was there only to bust the IR budget and reach the AST
// emitter, and pruned to std/float alone the module routes "ir".
func f64MethodSrc(body string) string {
	return "import \"std/float\";\nfunction main(): i32 { " + body + " }\n"
}

// TestSelfHostF64MethodIR_X86_64 pins std/float's f64 receiver methods
// compiling + linking + running on the self-host x86-64 native-binary path
// (#4361, re-pointed off the deleted AST emitter by #3457 slice 5).
func TestSelfHostF64MethodIR_X86_64(t *testing.T) {
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

	for _, tc := range f64MethodCases {
		t.Run(tc.name, func(t *testing.T) {
			src := f64MethodSrc(tc.body)
			want := interpExit(t, interpBin, src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			// Assert the module routes IR — the only path there is now.
			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (import set now busts the IR budget — prune it)", tc.name, got)
			}

			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
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
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
