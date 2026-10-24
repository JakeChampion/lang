package e2ecompiler

import (
	"bytes"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostCheckerDriverX86_64(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "drivers/checker_run.fern", false)

	cases := []struct {
		name     string
		src      string
		wantExit int
		wantDiag string
	}{
		{"well-typed-arith", "function main(): i32 { return 1 + 2; }\n", 0, ""},
		{"well-typed-vars", "function main(): i32 { let a: i32 = 5; let b: i32 = a + 1; return b; }\n", 0, ""},
		{"return-type-mismatch", "function main(): i32 { let s: string = \"x\"; return s; }\n", 1, "error[E002]"},
		{"arith-type-mismatch", "function main(): i32 { let s: string = \"x\"; return 1 + s; }\n", 1, "error["},
		{"field-assign-e048", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p.x = 5; return p.x; }\n", 1, "error[E048]"},
		{"subscript-assign-e056", "function main(): i32 { let a: i32[] = [1, 2, 3]; a[0] = 9; return a[0]; }\n", 1, "error[E056]"},
		{"struct-destructure", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p: P = P { x: 1, y: 2 }; let P { x, y } = p; return x + y; }\n", 0, ""},
		{"struct-destructure-single-field", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p: P = P { x: 1, y: 2 }; let P { x, .. } = p; return x; }\n", 0, ""},
		{"at-binding-tuple", "function main(): i32 { let w @ (a, b) = (1, 2); return w.0 + a + b; }\n", 0, ""},
		{"at-binding-struct", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p: P = P { x: 1, y: 2 }; let w @ P { x, y } = p; return w.x + x + y; }\n", 0, ""},
		{"for-struct-pattern", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let ps: P[] = [P { x: 1, y: 2 }]; let acc: i32 = 0; for P { x, y } in ps { acc = acc + x + y; } return acc; }\n", 0, ""},
		{"nested-tuple-destructure", "function main(): i32 { let (a, (b, c)) = (1, (2, 3)); return a + b + c; }\n", 0, ""},
		{"destructure-arity-e024", "function main(): i32 { let (a, b, c) = (1, 2); return a; }\n", 1, "error[E024]"},
		{"destructure-non-tuple-e024", "function main(): i32 { let (a, b) = 5; return a; }\n", 1, "error[E024]"},
		{"for-over-struct-e083", "struct Box { k: i32 }\nfunction main(): i32 {\n  let b: Box = Box { k: 1 };\n  let n: i32 = 0;\n  for it in b {\n    n = n + it.k;\n  }\n  return n;\n}\n", 1, "error[E083]: cannot iterate over a value of type Box: `for` walks an array, a string, a Map, a range, an iter.Iterator, or a value whose iter() returns one (5:13)"},
		{"destructure-field-type-e002", "struct P { x: string }\nfunction main(): i32 { let p: P = P { x: \"s\" }; let P { x } = p; return x; }\n", 1, "error[E002]"},
		{"value-match-settled-local", valueMatchSettledLocal, 0, ""},
		{"method-callable-param-binds", methodCallableParamBinds, 0, ""},
		{"generic-enum-callable-payload", genericEnumCallablePayload, 0, ""},
		{"cast-struct-to-other-struct-e033", "struct Point { x: i32, y: i32 }\nstruct Vec2 { x: i32, y: i32 }\nfunction f(p: Point): Vec2 { return p as Vec2; }\nfunction main(): i32 { return 0; }\n", 1, "error[E033]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: checker exited %d, want %d\nstderr: %s", tc.name, code, tc.wantExit, stderr.String())
			}
			if tc.wantDiag != "" && !strings.Contains(stderr.String(), tc.wantDiag) {
				t.Errorf("%s: stderr missing %q\ngot stderr: %s", tc.name, tc.wantDiag, stderr.String())
			}
		})
	}
}

func TestSelfHostCheckerDriverArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	files := map[string]string{}
	for _, p := range selfHostImportClosureFiles(t, "drivers/checker_run.fern") {
		if filepath.Base(p) == filepath.Base("drivers/checker_run.fern") {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		files[filepath.Base(p)] = e2eharness.FlatDriverSource(string(src))
	}
	runSrc, err := os.ReadFile("../../../compiler/drivers/checker_run.fern")
	if err != nil {
		t.Fatalf("read checker_run.fern: %v", err)
	}
	files["main.fern"] = e2eharness.FlatDriverSource(string(runSrc))

	checkerAsm, progDir := compileFilesModload(t, x86runner, driverBin, files, "-target", "arm64-linux")
	if len(checkerAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the checker driver")
	}
	checkerBin := buildBin(t, arm64gcc, progDir, "checker", checkerAsm)

	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{"well-typed", "function main(): i32 { return 1 + 2; }\n", 0},
		{"mismatch", "function main(): i32 { let s: string = \"x\"; return s; }\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runArm64Bin(qemu, checkerBin)
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: checker exited %d, want %d", tc.name, code, tc.wantExit)
			}
		})
	}
}
