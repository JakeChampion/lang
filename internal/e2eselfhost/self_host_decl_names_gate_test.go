package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSelfHostDeclNamesGate pins that the self-host drivers REJECT a declaration
// with no name, or a parameter with no type, rather than carrying a nameless function through the pipeline.
//
// The self-host parser is deliberately permissive — `Par` has no error channel,
// so a token it cannot use is skipped and the drivers' gates catch the
// consequences. A declaration NAME leaves no consequence to catch:
// `peek_member_name` returns "" for a keyword, `parse_func_decl` returns a
// FuncDecl with an empty name, and the module parses "successfully".
//
// `function use()` is the case that cost us: `use` is in the lexer's keyword set, so
// the function came out nameless and every downstream stage reported
// verdicts about a malformed module. Two sessions of #3457 read those verdicts as
// evidence about the IR subset and wrote a WRONG bisection into
// docs/SELFHOST-AST-RETIREMENT.md before anyone noticed the eligibility report
// listing a function called "".
//
// The native parser has always rejected the same source properly, which is what
// makes this worth a test: the divergence is the bug. Each case below asserts the
// native compiler AND the self-host driver both refuse, so the two cannot drift
// apart again.
func TestSelfHostDeclNamesGate(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostFiles(t, dir, "lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	for _, tc := range []struct {
		name   string
		src    string
		reject bool
		cause  string
	}{
		// `use` is the one that actually cost us. The others are keywords a
		// reasonable person might reach for as an identifier.
		{"keyword-use", "function use(): i32 { return 1; }\nfunction main(): i32 { return use(); }", true, "malformed function declaration"},
		{"keyword-type", "function type(): i32 { return 1; }\nfunction main(): i32 { return type(); }", true, "malformed function declaration"},
		{"keyword-match", "function match(): i32 { return 1; }\nfunction main(): i32 { return match(); }", true, "malformed function declaration"},
		{"keyword-impl", "function impl(): i32 { return 1; }\nfunction main(): i32 { return impl(); }", true, "malformed function declaration"},

		// Controls: ordinary names, a name that merely CONTAINS a keyword, and a
		// receiver method — the gate must not fire on any of them.
		//
		// NOT covered here: `function (s: S) default()`. The self-host parser
		// accepts `default` as a member name (peek_member_name allows it
		// deliberately) and the native parser rejects it. That is a real
		// divergence, but a different one — the name is present, not empty — so
		// asserting agreement on it here would fail for an unrelated reason.
		// A parameter with no type (#10260): the parser records it with an
		// empty type, on a free function, a method and a trait requirement.
		{"untyped-param", "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }", true, "has no type"},
		{"untyped-impl-self", "trait Conv { function conv(self: Self): i32; }\nstruct A { v: i32 }\nimpl Conv for A { function conv(self): i32 { return 1; } }\nfunction main(): i32 { return 0; }", true, "has no type"},
		{"untyped-trait-requirement", "trait Conv { function conv(self): i32; }\nfunction main(): i32 { return 0; }", true, "has no type"},
		// A local function's FuncDecl is desugared to a closure, so the parser
		// reports its untyped parameter through a sentinel instead.
		{"untyped-local-fn", "function outer(): i32 {\n    function g(y): i32 { return 0; }\n    return g(1);\n}\nfunction main(): i32 { return outer(); }", true, "has no type"},
		// A parser sentinel: wasm_run has no checked prologue to report it.
		{"sentinel", "function main(): i32 { return @; }", true, "parser-side unknown"},

		{"ordinary-name", "function helper(): i32 { return 42; }\nfunction main(): i32 { return helper(); }", false, ""},
		{"name-containing-keyword", "function usenow(): i32 { return 42; }\nfunction main(): i32 { return usenow(); }", false, ""},
		{"receiver-method", "struct S { }\nfunction (s: S) twice(): i32 { return 42; }\nfunction main(): i32 { var s = S { }; return s.twice(); }", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")

			// The native compiler is the oracle for what is legal Fern.
			nativeOK := exec.Command(interpBin, "-check", writeTemp(t, dir, tc.name+".fern", src)).Run() == nil
			if nativeOK == tc.reject {
				t.Fatalf("native -check accepted=%v, want accepted=%v — the fixture no longer means what it says", nativeOK, !tc.reject)
			}

			out, stderr, code := runDeclGate(t, runner, driverBin, src)
			if tc.reject {
				if code == 0 || len(out) != 0 {
					t.Fatalf("driver exited %d with %d bytes, want a refusal — a nameless declaration reached the emitter", code, len(out))
				}
				if !strings.Contains(stderr, tc.cause) {
					t.Errorf("refusal did not name the cause:\n%s", stderr)
				}
				return
			}
			if code != 0 || len(out) == 0 {
				t.Fatalf("driver exited %d with %d bytes for a legal program\n%s", code, len(out), stderr)
			}
		})
	}
}

// TestSelfHostDeclNamesGateNativeX86_64 is the same refusal on the x86-64
// stdin driver, which calls the gate before emitting through asm_ir.
func TestSelfHostDeclNamesGateNativeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	for _, tc := range []struct{ name, src, cause string }{
		{"untyped-param", "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }", "has no type"},
		{"untyped-impl-self", "trait Conv { function conv(self: Self): i32; }\nstruct A { v: i32 }\nimpl Conv for A { function conv(self): i32 { return 1; } }\nfunction main(): i32 { return 0; }", "has no type"},
		{"untyped-local-fn", "function outer(): i32 {\n    function g(y): i32 { return 0; }\n    return g(1);\n}\nfunction main(): i32 { return outer(); }", "has no type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, code := runDeclGate(t, runner, driverBin, []byte(tc.src+"\n"))
			if code == 0 || len(out) != 0 {
				t.Fatalf("driver exited %d with %d bytes, want a refusal before codegen", code, len(out))
			}
			if !strings.Contains(stderr+string(out), tc.cause) {
				t.Errorf("refusal did not name the cause:\n%s", stderr)
			}
		})
	}
	out, stderr, code := runDeclGate(t, runner, driverBin, []byte("function main(): i32 { return 42; }\n"))
	if code != 0 || len(out) == 0 {
		t.Fatalf("driver exited %d with %d bytes for a legal program\n%s", code, len(out), stderr)
	}
}

// TestSelfHostDeclNamesGateRawPathsX86_64 covers the driver paths that emit or
// report without the emitters' checked prologue: asm_ir_run's `-ir` fast path,
// and the loading drivers' merged, over-budget, per-module, probe and decide
// paths. Each refuses an untyped parameter, alone or beside a parser sentinel,
// and a sentinel alone wherever the checker does not report it first.
func TestSelfHostDeclNamesGateRawPathsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	irDir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, irDir, "asm_ir_run.fern")
	irBin := buildSelfHostBin(t, gcc, irDir, "asm_ir_run.fern", "driver")

	const untyped = "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }\n"
	const sentinel = "function main(): i32 { return @; }\n"
	const both = "function f(x): i32 { return @; }\nfunction main(): i32 { return f(1); }\n"
	for _, tc := range []struct{ name, src, cause string }{
		{"untyped", untyped, "has no type"},
		{"sentinel", sentinel, "parser-side unknown"},
		{"sentinel-and-untyped", both, "parser-side unknown"},
	} {
		t.Run("asm_ir_run-ir/"+tc.name, func(t *testing.T) {
			out, stderr, code := runDeclGate(t, runner, irBin, []byte(tc.src), "-ir")
			if code == 0 || len(out) != 0 {
				t.Fatalf("driver exited %d with %d bytes, want a refusal before codegen", code, len(out))
			}
			if !strings.Contains(stderr, tc.cause) {
				t.Errorf("refusal did not name the cause:\n%s", stderr)
			}
		})
	}
	out, stderr, code := runDeclGate(t, runner, irBin, []byte("function main(): i32 { return 42; }\n"), "-ir")
	if code != 0 || len(out) == 0 {
		t.Fatalf("asm_ir_run -ir exited %d with %d bytes for a legal program\n%s", code, len(out), stderr)
	}

	if len(runner) != 0 {
		t.Skip("the loading drivers run natively; skipping under an exec runner")
	}
	copySelfHostDriver(t, irDir, "asm_load_run.fern")
	loaders := []struct {
		name  string
		bin   string
		modes [][]string
	}{
		{"asm_load_run", buildSelfHostBin(t, gcc, irDir, "asm_load_run.fern", "load"), [][]string{nil, {"-per-module-count"}, {"-ir-probe"}, {"-decide"}}},
		{"asm_modload_run", buildSelfHostBin(t, gcc, writeSelfHostModloadProject(t), "asm_modload_run.fern", "modload"), [][]string{nil, {"-per-module-count"}, {"-ir-probe"}}},
		{"wasm_modload_run", buildWasmModloadDriver(t, gcc), [][]string{{"-per-module-emit", "0"}, {"-per-module-count"}}},
	}
	// Past the 512-function IR budget the default path emits per module, which
	// skips the emitters' prologue; a sentinel must still be refused there.
	var overBudget strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&overBudget, "function filler%d(): i32 { return %d; }\n", i, i)
	}
	overBudget.WriteString(sentinel)
	stage := t.TempDir()
	for _, ld := range loaders {
		t.Run(ld.name+"/over-budget-sentinel", func(t *testing.T) {
			mainPath := writeTemp(t, stage, "over_budget.fern", []byte(overBudget.String()))
			cmd := exec.Command(ld.bin, append([]string{mainPath}, ld.modes[0]...)...)
			combined, _ := cmd.CombinedOutput()
			if cmd.ProcessState.ExitCode() == 0 {
				t.Fatalf("driver emitted %d bytes for an over-budget module holding a sentinel", len(combined))
			}
			if !strings.Contains(string(combined), "parser-side unknown") {
				t.Errorf("refusal did not name the cause:\n%.2000s", combined)
			}
		})
		for _, tc := range []struct{ name, src, cause string }{
			{"untyped", untyped, "has no type"},
			{"sentinel-and-untyped", both, "has no type"},
			{"sentinel", sentinel, "parser-side unknown"},
			// The one sentinel with a native code names it on every driver,
			// whether or not a checker runs before the gate.
			{"valueless-block", "function side(): i32 { return 1; }\nfunction main(): i32 {\n    var x: i32 = { side(); };\n    return x;\n}\n", "E061"},
		} {
			mainPath := writeTemp(t, stage, tc.name+".fern", []byte(tc.src))
			for _, args := range ld.modes {
				t.Run(ld.name+"/"+tc.name+"/"+strings.Join(args, ""), func(t *testing.T) {
					cmd := exec.Command(ld.bin, append([]string{mainPath}, args...)...)
					combined, _ := cmd.CombinedOutput()
					if cmd.ProcessState.ExitCode() == 0 {
						t.Fatalf("driver accepted a malformed module:\n%s", combined)
					}
					if !strings.Contains(string(combined), tc.cause) {
						t.Errorf("refusal did not name the cause:\n%s", combined)
					}
				})
			}
		}
	}
}

// TestSelfHostDeclNamesGateWasmStdinX86_64 covers the wasm stdin drivers
// TestSelfHostDeclNamesGate does not build, and the playground's emit and
// -check modes. None needs wasmtime: each refusal comes before any module is
// emitted.
func TestSelfHostDeclNamesGateWasmStdinX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	copySelfHostDriver(t, dir, "wasm_runio_run.fern")
	drivers := []struct {
		name string
		bin  string
		args []string
	}{
		{"wasm_ir_run", buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run"), nil},
		{"wasm_ir_run-ir", "", []string{"-ir"}},
		{"wasm_runio_run", buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run"), nil},
		{"wasm_runio_run-decide", "", []string{"-decide"}},
	}
	drivers[1].bin, drivers[3].bin = drivers[0].bin, drivers[2].bin
	cases := []struct{ name, src, cause string }{
		{"untyped-param", "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }\n", "has no type"},
		{"untyped-trait-requirement", "trait Conv { function conv(self): i32; }\nfunction main(): i32 { return 0; }\n", "has no type"},
		{"sentinel", "function main(): i32 { return @; }\n", "parser-side unknown"},
		{"untyped-local-fn", "function outer(): i32 {\n    function g(y): i32 { return 0; }\n    return g(1);\n}\nfunction main(): i32 { return outer(); }\n", "has no type"},
	}
	for _, d := range drivers {
		for _, tc := range cases {
			t.Run(d.name+"/"+tc.name, func(t *testing.T) {
				out, stderr, code := runDeclGate(t, runner, d.bin, []byte(tc.src), d.args...)
				if code == 0 || len(out) != 0 {
					t.Fatalf("driver exited %d with %d bytes, want a refusal", code, len(out))
				}
				if !strings.Contains(stderr, tc.cause) {
					t.Errorf("refusal did not name the cause:\n%s", stderr)
				}
			})
		}
	}
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	pg := buildPlaygroundDriver(t)
	for _, mode := range [][]string{nil, {"-check"}} {
		for _, tc := range cases {
			t.Run("playground_run"+strings.Join(mode, "")+"/"+tc.name, func(t *testing.T) {
				out, stderr, code := runPlayground(t, pg, t.TempDir(), tc.src, mode...)
				if code == 0 || len(out) != 0 {
					t.Fatalf("playground exited %d with %d bytes, want a refusal", code, len(out))
				}
				if !strings.Contains(stderr, tc.cause) {
					t.Errorf("refusal did not name the cause:\n%s", stderr)
				}
			})
		}
	}
}

// TestSelfHostEmittingDriversRunDeclGates pins that every self-host driver
// calling an emitter entry point also reports parser sentinels and runs the
// declaration gate. The gates cannot live in the emitters, which see the module
// after the local-function lift has added untyped capture parameters, so each
// driver carries its own call and a new driver must too.
func TestSelfHostEmittingDriversRunDeclGates(t *testing.T) {
	emits := regexp.MustCompile(`\b(asm_ir|asm_arm64_ir|wasm_ir)\.emit_\w*\(`)
	declGate := regexp.MustCompile(`\b(refuse_decl_names|check_decl_names)\(`)
	sentinelGate := regexp.MustCompile(`\b(refuse_parse_unknowns|parse_unknown_errors_module)\(`)
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "self_host", "*.fern"))
	if err != nil || len(files) == 0 {
		t.Fatalf("globbing the self-host sources: %v (%d files)", err, len(files))
	}
	drivers := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		var code strings.Builder
		for _, line := range strings.Split(string(raw), "\n") {
			if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "//") {
				code.WriteString(line + "\n")
			}
		}
		src := code.String()
		if !emits.MatchString(src) {
			continue
		}
		drivers++
		if !declGate.MatchString(src) {
			t.Errorf("%s calls an emitter but never runs the declaration gate", filepath.Base(f))
		}
		if !sentinelGate.MatchString(src) {
			t.Errorf("%s calls an emitter but never reports parser sentinels", filepath.Base(f))
		}
	}
	if drivers < 10 {
		t.Fatalf("found %d emitting drivers, expected the full set: a silently shrunken sweep proves nothing", drivers)
	}
}

func buildWasmModloadDriver(t *testing.T, gcc string) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_modload_run.fern")
	return buildSelfHostBin(t, gcc, dir, "wasm_modload_run.fern", "wasm_modload_run")
}

func writeTemp(t *testing.T, dir, name string, src []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// runDeclGate runs the driver returning stdout, stderr and the exit code — the
// gate cases expect a non-zero exit, which runCapture would fatal on.
func runDeclGate(t *testing.T, runner []string, bin string, stdin []byte, args ...string) ([]byte, string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, args...)
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), bin), args...)...)
	}
	cmd.Stdin = strings.NewReader(string(stdin))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return []byte(stdout.String()), stderr.String(), cmd.ProcessState.ExitCode()
}
