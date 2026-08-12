package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostDeclNamesGate pins that the self-host drivers REJECT a declaration
// with no name, rather than carrying a nameless function through the pipeline.
//
// The self-host parser is deliberately permissive — `Par` has no error channel,
// so a token it cannot use is skipped and the drivers' gates catch the
// consequences. A declaration NAME leaves no consequence to catch:
// `peek_member_name` returns "" for a keyword, `parse_func_decl` returns a
// FuncDecl with an empty name, and the module parses "successfully".
//
// `function use()` is the case that bit: `use` is in the lexer's keyword set, so
// the function came out nameless and every downstream stage happily reported
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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, rerr := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		if werr := os.WriteFile(filepath.Join(dir, name), src, 0o644); werr != nil {
			t.Fatalf("write %s: %v", name, werr)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	for _, tc := range []struct {
		name   string
		src    string
		reject bool
	}{
		// `use` is the one that actually cost us. The others are keywords a
		// reasonable person might reach for as an identifier.
		{"keyword-use", "function use(): i32 { return 1; }\nfunction main(): i32 { return use(); }", true},
		{"keyword-type", "function type(): i32 { return 1; }\nfunction main(): i32 { return type(); }", true},
		{"keyword-match", "function match(): i32 { return 1; }\nfunction main(): i32 { return match(); }", true},
		{"keyword-impl", "function impl(): i32 { return 1; }\nfunction main(): i32 { return impl(); }", true},

		// Controls: ordinary names, a name that merely CONTAINS a keyword, and a
		// receiver method — the gate must not fire on any of them.
		//
		// NOT covered here: `function (s: S) default()`. The self-host parser
		// accepts `default` as a member name (peek_member_name allows it
		// deliberately) and the native parser rejects it. That is a real
		// divergence, but a different one — the name is present, not empty — so
		// asserting agreement on it here would fail for an unrelated reason.
		{"ordinary-name", "function helper(): i32 { return 42; }\nfunction main(): i32 { return helper(); }", false},
		{"name-containing-keyword", "function usenow(): i32 { return 42; }\nfunction main(): i32 { return usenow(); }", false},
		{"receiver-method", "struct S { }\nfunction (s: S) twice(): i32 { return 42; }\nfunction main(): i32 { var s = S { }; return s.twice(); }", false},
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
				if !strings.Contains(stderr, "has no name") {
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
func runDeclGate(t *testing.T, runner []string, bin string, stdin []byte) ([]byte, string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = strings.NewReader(string(stdin))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return []byte(stdout.String()), stderr.String(), cmd.ProcessState.ExitCode()
}
