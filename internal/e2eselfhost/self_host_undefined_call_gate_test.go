package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runDriverAllowFail runs a driver binary and returns (stdout, stderr, exit
// code) WITHOUT failing the test on a non-zero exit — the reject cases below
// are exactly the non-zero exits (runDriverFile fatals on them).
func runDriverAllowFail(t *testing.T, runner []string, bin string, stdin string, args ...string) ([]byte, []byte, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, args...)
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), bin), args...)...)
	}
	if stdin != "" {
		cmd.Stdin = bytes.NewReader([]byte(stdin))
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, _ := cmd.Output()
	return out, stderr.Bytes(), cmd.ProcessState.ExitCode()
}

// TestSelfHostUndefinedCallGate pins #5644 on the file-based x86 driver: a
// call to a function that does not exist must REJECT (exit 1 + an E001
// diagnostic naming the callee, nothing emitted) instead of emitting
// `call __fn_<name>` against nothing and exiting 0 — which turned a source
// typo into a bare `undefined reference` from the linker. An import that
// resolves to no file is the same defect one level up: modloader skips it, so
// every qualified call into it dangles; that case gets the module named.
//
// The accept cases are the gate's real risk: they pin that the admit list
// (builtins, the emitter-only free-function spellings, variant constructors,
// closure locals, receiver methods) never rejects a valid program, and that an
// accepted program still routes through the IR path and runs.
func TestSelfHostUndefinedCallGate(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	// compile writes `main.fern` (plus builtins.fern, which the driver merges)
	// into a fresh dir and runs the driver on it.
	compile := func(t *testing.T, src string) ([]byte, []byte, int, string) {
		t.Helper()
		dir := t.TempDir()
		bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
		if err != nil {
			t.Fatalf("read builtins.fern: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "builtins.fern"), bsrc, 0o644); err != nil {
			t.Fatalf("write builtins.fern: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(src), 0o644); err != nil {
			t.Fatalf("write main.fern: %v", err)
		}
		out, errOut, code := runDriverAllowFail(t, runner, driverBin, "", filepath.Join(dir, "main.fern"))
		return out, errOut, code, dir
	}

	rejects := []struct {
		name string
		src  string
		want []string // substrings the diagnostic must carry
	}{
		// The issue's repro: exit 0 + 1662 bytes of asm containing
		// `call __fn_totally_undefined_fn` before the gate.
		{
			"undefined-fn",
			"function main(): i32 {\n    return totally_undefined_fn(1);\n}\n",
			[]string{"E001", "totally_undefined_fn"},
		},
		// An undefined callee reached only from a nested position (a lambda
		// body inside an argument) — the walk must find it there too.
		{
			"undefined-in-lambda",
			"function apply(f: (i32) => i32, n: i32): i32 { return f(n); }\n" +
				"function main(): i32 { return apply(function (n: i32): i32 { return missing_helper(n); }, 1); }\n",
			[]string{"E001", "missing_helper"},
		},
		// Unresolved import (#5644 comment 2): `std/jni` resolves to no file
		// from this dir, so the qualified call becomes a dangling
		// `__fn_jni__call0`. The message must blame the MODULE, not the name.
		{
			"unresolved-import",
			"import \"std/jni\";\n" +
				"function jniVersion(env: usize, cls: usize): usize { return jni.call0(env, 4); }\n" +
				"function main(): i32 { return 0; }\n",
			[]string{"E001", "jni__call0", "no module 'jni' was loaded"},
		},
	}
	for _, tc := range rejects {
		t.Run("reject-"+tc.name, func(t *testing.T) {
			out, errOut, code, _ := compile(t, tc.src)
			if code != 1 {
				t.Errorf("driver exited %d, want 1 (reject)", code)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(errOut), want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, want)
				}
			}
			if len(out) != 0 {
				t.Errorf("driver emitted %d bytes for an undefined-call program, want 0", len(out))
			}
		})
	}

	// One accept program exercising most admit-list arms at once: builtins
	// (`print` / `len`), the emitter-only free-function spellings
	// (`i32_to_string` / `str_to_upper`, which native's checker rejects but
	// every self-host emitter lowers), an Option constructor, a user enum
	// variant constructor, and a receiver method.
	const acceptSrc = `enum Shape { Circle(i32), Square(i32) }
struct P { x: i32, y: i32 }
function (p: P) sum(): i32 { return p.x + p.y; }
function dbl(n: i32): i32 { return n * 2; }
function area(s: Shape): i32 {
    match (s) {
        Circle(r) => { return r * 3; },
        Square(w) => { return w * w; },
    }
}
function main(): i32 {
    var p: P = P { x: 4, y: 3 };
    var xs: i32[] = [1, 2, 3];
    print(i32_to_string(area(Circle(2))) + str_to_upper("ok"));
    match (Some(dbl(p.sum()))) {
        Some(v) => { return v + len(xs) + xs.len(); },
        None => { return 1; },
    }
}
`
	t.Run("accept", func(t *testing.T) {
		asm, errOut, code, dir := compile(t, acceptSrc)
		if code != 0 {
			t.Fatalf("driver exited %d (stderr %q), want 0 (accept)", code, errOut)
		}
		if !strings.Contains(string(asm), ".Lir") {
			t.Fatal("accepted program did not route through the IR path (no .Lir labels)")
		}
		bin := buildBin(t, gcc, dir, "accept", string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
		}
		out, exit := runBin(cmd, "")
		if exit != 20 { // dbl(7) = 14, + len(xs) 3 + xs.len() 3
			t.Errorf("program exited %d, want 20 (stdout %q)", exit, out)
		}
		if !strings.Contains(out, "6OK") {
			t.Errorf("stdout = %q, want it to contain %q", out, "6OK")
		}
	})

	// A local closure called by bare name is the shadow-set arm: `f` names no
	// module function, so only the enclosing function's binder set keeps the
	// gate off it. (This shape bails to the AST emitter on `const_func`, so it
	// pins acceptance, not IR routing.)
	t.Run("accept-closure", func(t *testing.T) {
		_, errOut, code, _ := compile(t, "function dbl(n: i32): i32 { return n * 2; }\n"+
			"function main(): i32 { var f = dbl; return f(21); }\n")
		if code != 0 {
			t.Fatalf("driver exited %d (stderr %q), want 0 (accept)", code, errOut)
		}
	})

	// A stdlib-importing program is the false-positive case that matters most:
	// its calls are mangled (`math__range`), so the gate sees names that only
	// resolve because modload really loaded the module — the same shape that
	// dangles when an import resolves to nothing.
	t.Run("accept-stdlib", func(t *testing.T) {
		asm, _ := compileSourceModload(t, runner, driverBin,
			"import \"std/math\";\nfunction main(): i32 { return math.range(0, 7).len(); }\n")
		if !strings.Contains(asm, "__fn_main") {
			t.Fatalf("stdlib program emitted no main (%d bytes)", len(asm))
		}
	})
}

// TestSelfHostWasmUndefinedCallGate is the wasm sibling: the same gate runs on
// both wasm drivers, so an undefined callee is a diagnostic instead of a WAT
// that fails at load with `unknown func`.
func TestSelfHostWasmUndefinedCallGate(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern",
		"wasm_run.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	drivers := []struct {
		name string
		bin  string
		args []string
	}{
		{"wasm_run", buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run"), nil},
		{"wasm_ir_run", buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run"), []string{"-ir"}},
	}
	const rejectSrc = "function main(): i32 { return totally_undefined_fn(1); }"
	const acceptSrc = "function dbl(n: i32): i32 { return n * 2; }\n" +
		"function main(): i32 { var f = dbl; return f(21); }"
	for _, d := range drivers {
		t.Run(d.name+"/reject", func(t *testing.T) {
			out, errOut, code := runDriverAllowFail(t, runner, d.bin, rejectSrc, d.args...)
			if code != 1 {
				t.Errorf("driver exited %d, want 1 (reject)", code)
			}
			if !strings.Contains(string(errOut), "E001") || !strings.Contains(string(errOut), "totally_undefined_fn") {
				t.Errorf("stderr = %q, want an E001 naming the callee", errOut)
			}
			if len(out) != 0 {
				t.Errorf("driver emitted %d bytes for an undefined-call program, want 0", len(out))
			}
		})
		t.Run(d.name+"/accept", func(t *testing.T) {
			out, errOut, code := runDriverAllowFail(t, runner, d.bin, acceptSrc, d.args...)
			if code != 0 {
				t.Fatalf("driver exited %d (stderr %q), want 0 (accept)", code, errOut)
			}
			if len(out) == 0 {
				t.Fatal("driver emitted 0 bytes for a valid program")
			}
		})
	}
}
