package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostDeclNamesGate(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostFiles(t, dir, "lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	for _, tc := range []struct {
		name   string
		src    string
		reject bool
		cause  string
	}{
		{"keyword-use", "function use(): i32 { return 1; }\nfunction main(): i32 { return use(); }", true, "expected a name, got"},
		{"keyword-type", "function type(): i32 { return 1; }\nfunction main(): i32 { return type(); }", true, "expected a name, got"},
		{"keyword-match", "function match(): i32 { return 1; }\nfunction main(): i32 { return match(); }", true, "expected a name, got"},
		{"keyword-impl", "function impl(): i32 { return 1; }\nfunction main(): i32 { return impl(); }", true, "expected a name, got"},

		{"untyped-param", "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }", true, "expected \":\", got"},
		{"untyped-impl-self", "trait Conv { function conv(self: Self): i32; }\nstruct A { v: i32 }\nimpl Conv for A { function conv(self): i32 { return 1; } }\nfunction main(): i32 { return 0; }", true, "expected \":\", got"},
		{"untyped-trait-requirement", "trait Conv { function conv(self): i32; }\nfunction main(): i32 { return 0; }", true, "expected \":\", got"},
		{"keyword-trait-requirement", "trait Conv { function use(): i32; }\nfunction main(): i32 { return 0; }", true, "expected a name, got"},
		{"untyped-local-fn", "function outer(): i32 {\n    function g(y): i32 { return 0; }\n    return g(1);\n}\nfunction main(): i32 { return outer(); }", true, "expected \":\", got"},
		{"keyword-local-fn", "function outer(): i32 {\n    function use(): i32 { return 0; }\n    return 1;\n}\nfunction main(): i32 { return outer(); }", true, "expected a name, got"},
		{"untyped-destructure-local-fn", "function outer(): i32 {\n    function g((a, b)): i32 { return a; }\n    return 1;\n}\nfunction main(): i32 { return outer(); }", true, "its name or signature could not be read"},
		{"singleton-destructure-local-fn", "function outer(): i32 {\n    function g((a): (i32)): i32 { return a; }\n    return 1;\n}\nfunction main(): i32 { return outer(); }", true, "its name or signature could not be read"},
		{"trait-assoc-type-nameless", "trait T { type 123; }\nfunction main(): i32 { return 0; }", true, "malformed associated type declaration: its name could not be read"},
		{"trait-stray-token", "trait T { 123 }\nfunction main(): i32 { return 0; }", true, "unexpected token in a trait body"},
		{"impl-assoc-binding-nameless", "trait T { type Item; }\nstruct A { x: i32 }\nimpl T for A { type 123 = i32; }\nfunction main(): i32 { return 0; }", true, "malformed associated type binding: its name could not be read"},
		{"impl-stray-token", "trait T { }\nstruct A { x: i32 }\nimpl T for A { 123 }\nfunction main(): i32 { return 0; }", true, "unexpected token in an impl body"},
		{"import-alias-keyword", "import \"./lib\" as use;\nfunction main(): i32 { return 0; }", true, "malformed import alias: its name could not be read"},
		{"pub-use-keyword", "pub use \"./lib\".{use};\nfunction main(): i32 { return 0; }", true, "expected a name, got \"use\""},
		{"import-resource-nameless", "@import(\"wasi:io@0.2.0\", \"thing\") resource 123;\nfunction main(): i32 { return 0; }", true, "@import only applies to a function or resource declaration"},
		{"unknown-attribute", "@foo\nfunction f(): i32 { return 1; }\nfunction main(): i32 { return f(); }", true, "unknown attribute @foo"},
		{"keyword-impl-trait", "struct A { x: i32 }\nimpl use for A { }\nfunction main(): i32 { return 0; }", true, "malformed impl: its trait name could not be read"},
		{"pathless-import", "import;\nfunction main(): i32 { return 0; }", true, "malformed import: its path could not be read"},
		{"pathless-reexport", "pub use;\nfunction main(): i32 { return 0; }", true, "malformed re-export: its path could not be read"},
		{"pathless-import-list", "import x.{a};\nfunction main(): i32 { return 0; }", true, "malformed import: its path could not be read"},
		{"pathless-reexport-list", "pub use x.{a};\nfunction main(): i32 { return 0; }", true, "malformed re-export: its path could not be read"},
		{"primitive-impl-type", "trait T { function f(self: Self): i32; }\nimpl T for i32 { function f(self: i32): i32 { return self; } }\nfunction main(): i32 { return 0; }", false, ""},
		{"sentinel", "function main(): i32 { return @; }", true, "expected an expression, got"},

		{"ordinary-name", "function helper(): i32 { return 42; }\nfunction main(): i32 { return helper(); }", false, ""},
		{"name-containing-keyword", "function usenow(): i32 { return 42; }\nfunction main(): i32 { return usenow(); }", false, ""},
		{"receiver-method", "struct S { }\nfunction (s: S) twice(): i32 { return 42; }\nfunction main(): i32 { let s = S { }; return s.twice(); }", false, ""},
		{"default-name", "struct S { }\nfunction (s: S) default(): i32 { return 42; }\nfunction main(): i32 { let s = S { }; return s.default(); }", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")

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

	for _, src := range []string{
		"import x.{a};\nfunction main(): i32 { return 0; }",
		"pub use x.{a};\nfunction main(): i32 { return 0; }",
		"function use(): i32 { return 1; }\nfunction main(): i32 { return 0; }",
		"function type(): i32 { return 1; }\nfunction main(): i32 { return 0; }",
		"@foo\nfunction f(): i32 { return 1; }\nfunction main(): i32 { return f(); }",
	} {
		_, stderr, _ := runDeclGate(t, runner, driverBin, []byte(src+"\n"))
		if n := strings.Count(stderr, "error["); n != 1 {
			t.Errorf("%q raised %d diagnostics, want 1:\n%s", src, n, stderr)
		}
	}
}

func TestSelfHostDeclNamesGateNativeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	for _, tc := range []struct{ name, src, cause string }{
		{"untyped-param", "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }", "expected \":\", got"},
		{"untyped-impl-self", "trait Conv { function conv(self: Self): i32; }\nstruct A { v: i32 }\nimpl Conv for A { function conv(self): i32 { return 1; } }\nfunction main(): i32 { return 0; }", "expected \":\", got"},
		{"untyped-local-fn", "function outer(): i32 {\n    function g(y): i32 { return 0; }\n    return g(1);\n}\nfunction main(): i32 { return outer(); }", "expected \":\", got"},
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

func TestSelfHostDeclNamesGateRawPathsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	irDir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, irDir, "drivers/asm_ir_run.fern")
	irBin := buildSelfHostBin(t, gcc, irDir, "drivers/asm_ir_run.fern", "driver")

	const untyped = "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }\n"
	const sentinel = "function main(): i32 { return @; }\n"
	const both = "function f(x): i32 { return @; }\nfunction main(): i32 { return f(1); }\n"
	for _, tc := range []struct{ name, src, cause string }{
		{"untyped", untyped, "expected \":\", got"},
		{"sentinel", sentinel, "expected an expression, got"},
		{"sentinel-and-untyped", both, "expected an expression, got"},
	} {
		t.Run("asm_ir_run/"+tc.name, func(t *testing.T) {
			out, stderr, code := runDeclGate(t, runner, irBin, []byte(tc.src))
			if code == 0 || len(out) != 0 {
				t.Fatalf("driver exited %d with %d bytes, want a refusal before codegen", code, len(out))
			}
			if !strings.Contains(stderr, tc.cause) {
				t.Errorf("refusal did not name the cause:\n%s", stderr)
			}
		})
	}
	out, stderr, code := runDeclGate(t, runner, irBin, []byte("function main(): i32 { return 42; }\n"))
	if code != 0 || len(out) == 0 {
		t.Fatalf("asm_ir_run exited %d with %d bytes for a legal program\n%s", code, len(out), stderr)
	}

	if len(runner) != 0 {
		t.Skip("the loading drivers run natively; skipping under an exec runner")
	}
	copySelfHostDriver(t, irDir, "drivers/asm_load_run.fern")
	loaders := []struct {
		name  string
		bin   string
		modes [][]string
	}{
		{"asm_load_run", buildSelfHostBin(t, gcc, irDir, "drivers/asm_load_run.fern", "load"), [][]string{nil, {"-per-module-count"}, {"-ir-probe"}, {"-decide"}}},
		{"asm_modload_run", buildSelfHostBin(t, gcc, writeSelfHostModloadProject(t), "drivers/asm_modload_run.fern", "modload"), [][]string{nil, {"-per-module-count"}, {"-ir-probe"}}},
		{"wasm_modload_run", buildWasmModloadDriver(t, gcc), [][]string{{"-per-module-emit", "0"}, {"-per-module-count"}}},
	}
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
			if !strings.Contains(string(combined), "expected an expression, got") {
				t.Errorf("refusal did not name the cause:\n%.2000s", combined)
			}
		})
		for _, tc := range []struct{ name, src, cause string }{
			{"untyped", untyped, "expected \":\", got"},
			{"sentinel-and-untyped", both, "expected \":\", got"},
			{"sentinel", sentinel, "expected an expression, got"},
			{"keyword-function", "function use(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", "expected a name, got \"use\" (1:10)"},
			{"keyword-trait-requirement", "trait Conv { function use(): i32; }\nfunction main(): i32 { return 0; }\n", "expected a name, got \"use\" (1:23)"},
			{"numeric-trait-name", "trait 123 { }\nfunction main(): i32 { return 0; }\n", "expected a name, got \"123\" (1:7)"},
			{"keyword-trait-name", "trait use { function conv(self: Self): i32; }\nfunction main(): i32 { return 0; }\n", "expected a name, got \"use\" (1:7)"},
			{"keyword-struct", "struct match { x: i32 }\nfunction main(): i32 { return 0; }\n", "expected a name, got \"match\" (1:8)"},
			{"numeric-struct-name", "struct 123 { x: i32 }\nfunction main(): i32 { return 0; }\n", "expected a name, got \"123\" (1:8)"},
			{"keyword-enum", "enum match { A, B }\nfunction main(): i32 { return 0; }\n", "expected a name, got \"match\" (1:6)"},
			{"keyword-const", "const use: i32 = 1;\nfunction main(): i32 { return 0; }\n", "expected a name, got \"use\" (1:7)"},
			{"pathless-import", "import;\nfunction main(): i32 { return 0; }\n", "malformed import: its path could not be read (1:7)"},
			{"keyword-alias", "type match = i32;\nfunction main(): i32 { return 0; }\n", "expected a name, got \"match\" (1:6)"},
			{"numeric-alias-name", "type 123 = i32;\nfunction main(): i32 { return 0; }\n", "expected a name, got \"123\" (1:6)"},
			{"valueless-block", "function side(): i32 { return 1; }\nfunction main(): i32 {\n    let x: i32 = { side(); };\n    return x;\n}\n", "E061"},
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

func TestSelfHostDeclNamesGateWasmStdinX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	copySelfHostDriver(t, dir, "drivers/wasm_runio_run.fern")
	drivers := []struct {
		name string
		bin  string
		args []string
	}{
		{"wasm_ir_run", buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasm_ir_run"), nil},
		{"wasm_runio_run", buildSelfHostBin(t, gcc, dir, "drivers/wasm_runio_run.fern", "wasm_runio_run"), nil},
		{"wasm_runio_run-decide", "", []string{"-decide"}},
	}
	drivers[2].bin = drivers[1].bin
	cases := []struct{ name, src, cause string }{
		{"untyped-param", "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }\n", "expected \":\", got"},
		{"untyped-trait-requirement", "trait Conv { function conv(self): i32; }\nfunction main(): i32 { return 0; }\n", "expected \":\", got"},
		{"sentinel", "function main(): i32 { return @; }\n", "expected an expression, got"},
		{"untyped-local-fn", "function outer(): i32 {\n    function g(y): i32 { return 0; }\n    return g(1);\n}\nfunction main(): i32 { return outer(); }\n", "expected \":\", got"},
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

func TestSelfHostEmittingDriversRunDeclGates(t *testing.T) {
	emits := regexp.MustCompile(`\b(asm_ir|asm_arm64_ir|wasm_ir)\.emit_\w*\(|\bemitforms\.wasm_component\(`)
	hasMain := regexp.MustCompile(`(?m)^(pub )?function main\(`)
	parseGate := regexp.MustCompile(`\b(refuse_parse_errors|parse_errors|parse_errors_before_check)\(`)
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "compiler", "*.fern"))
	inDrivers, _ := filepath.Glob(filepath.Join("..", "..", "..", "compiler", "drivers", "*.fern"))
	files = append(files, inDrivers...)
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
		if !emits.MatchString(src) || !hasMain.MatchString(src) {
			continue
		}
		drivers++
		if !parseGate.MatchString(src) {
			t.Errorf("%s calls an emitter but never runs the parse gates", filepath.Base(f))
		}
	}
	if drivers < 10 {
		t.Fatalf("found %d emitting drivers, expected the full set: a silently shrunken sweep proves nothing", drivers)
	}
}

func buildWasmModloadDriver(t *testing.T, gcc string) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_modload_run.fern")
	return buildSelfHostBin(t, gcc, dir, "drivers/wasm_modload_run.fern", "wasm_modload_run")
}

func writeTemp(t *testing.T, dir, name string, src []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

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
