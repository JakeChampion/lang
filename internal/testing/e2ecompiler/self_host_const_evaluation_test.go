package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostConstEvaluationErrors(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, tc := range []struct{ name, declarations, message, code string }{
		{"division", `const A: i32 = 1 / 0;`, "division by zero", "E093"},
		{"modulo", `const A: i32 = 1 % 0;`, "modulo by zero", "E093"},
		{"earlier", `const D: i32 = 2 - 2; const A: i32 = 1 / D;`, "division by zero", "E093"},
		{"earlier-width", `const D: u8 = 255 + 1; const A: i64 = 1 / D;`, "division by zero", "E093"},
		{"wrapped-i32", `const A: i32 = 1 / ((2147483647 + 1) * 2);`, "division by zero", "E093"},
		{"wrapped-u32", `const A: u32 = 1 / (4294967295 + 1);`, "division by zero", "E093"},
		{"wrapped-u64", `const A: u64 = 1 / (18446744073709551615 + 1);`, "division by zero", "E093"},
		{"masked-shift", `const A: i32 = 1 / ((1 << 32) - 1);`, "division by zero", "E093"},
		{"unsigned-shift", `const A: u64 = 1 / ((18446744073709551615 >> 63) - 1);`, "division by zero", "E093"},
		{"float", `const A: f64 = 1.0 / (0.5 - 0.5);`, "division by zero", "E093"},
		{"negative-zero", `const A: f64 = 1.0 / -0.0;`, "division by zero", "E093"},
		{"dead-boolean", `const A: boolean = false && (1 / 0 == 0);`, "division by zero", "E093"},
		{"array", `const A: i32[] = [1, 2 / 0];`, "division by zero", "E093"},
		{"nested-array", `const A: i32[][] = [[1], [2 / 0]];`, "division by zero", "E093"},
		{"tuple", `const A: (i32, boolean) = (1 / 0, true);`, "division by zero", "E093"},
		{"struct", `struct S { n: i32 } const A: S = S { n: 1 / 0 };`, "division by zero", "E093"},
		{"mixed-operands", `const A: i32 = 1 + true;`, "operands aren't both numbers", "E093"},
		{"string-subtract", `const A: string = "a" - "b";`, "not allowed on strings", "E093"},
		{"bool-operator", `const A: boolean = true + false;`, "not allowed on bools", "E093"},
		{"float-operator", `const A: f64 = 1.0 % 2.0;`, "not allowed in float", "E093"},
		{"literal-priority", `const A: i32 = 2147483648 / 0;`, "literal 2147483648 does not fit in i32", "E047"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte(tc.declarations+"\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, frontend := range []struct{ name, bin string }{{"self", cli}, {"Go", oracle}} {
				for _, mode := range []string{"check", "interp", "build"} {
					t.Run(frontend.name+"/"+mode, func(t *testing.T) {
						args := []string{"-" + mode}
						output := filepath.Join(dir, frontend.name+"-"+mode)
						if mode == "build" {
							args = []string{"-target", "x86-64-linux", "-o", output}
						}
						args = append(args, path, stdlib)
						out, err := exec.Command(frontend.bin, args...).CombinedOutput()
						if err == nil || !strings.Contains(string(out), tc.message) {
							t.Fatalf("expected refusal with %q, got %v\n%s", tc.message, err, out)
						}
						if !strings.Contains(string(out), "error["+tc.code+"]") {
							t.Errorf("expected diagnostic %s\n%s", tc.code, out)
						}
						if tc.name == "mixed-operands" || tc.name == "string-subtract" {
							if strings.Count(string(out), "error[E093]") != 1 || !strings.Contains(string(out), ":1:1: error[E093]") {
								t.Errorf("want one constant diagnostic at its declaration:\n%s", out)
							}
						}
						if _, err := os.Stat(output); !os.IsNotExist(err) {
							t.Errorf("refused program left an executable: %v", err)
						}
					})
				}
			}
		})
	}
}

func TestSelfHostConstEvaluationValid(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, source string }{
		{"signed-width", `const A: i32 = (2147483647 + 1) / 2; function main(): i32 { if (A == -1073741824) { return 0; } return 1; }`},
		{"unsigned-width", `const A: u32 = 4294967295 / 2; function main(): i32 { if (A == 2147483647) { return 0; } return 1; }`},
		{"earlier-width", `const A: u8 = 255 + 2; const B: i64 = 10 / A; function main(): i32 { if (B == 10) { return 0; } return 1; }`},
		{"signed-overflow", `const A: i64 = (-9223372036854775807 - 1) / -1; function main(): i32 { if (A == -9223372036854775807 - 1) { return 0; } return 1; }`},
		{"aggregate-widths", `const A: (u8, i32[]) = (255 + 2, [(2147483647 + 1) / 2]); function main(): i32 { return 0; }`},
		{"boolean-string", `const S: string = "a" + "b"; const B: boolean = S == "ab" && !false; function main(): i32 { if (B) { return 0; } return 1; }`},
		{"float-fold-precision", `const D: f32 = 1e-30 * 1e-30; const A: f64 = 1.0 / D; function main(): i32 { return 0; }`},
		{"float-suffix-fold-precision", `const D: f32 = 1e-50f32; const A: f64 = 1.0 / D; function main(): i32 { return 0; }`},
		{"integer-literals-settle-to-float", `const A: f64 = 1 / 2; const N: f64 = -0; const F: f32 = 5; function main(): i32 { if (A == 0.5 && 1.0 / N < 0.0 && F == 5.0) { return 0; } return 1; }`},
		{"string-order", `const B: boolean = "ab" < "b" && "b" >= "b" && !("b" <= "ab"); function main(): i32 { if (B) { return 0; } return 1; }`},
		{"char-order", `const B: boolean = 'a' < 'b' && 'z' >= 'z' && b'a' + 1 == b'b'; function main(): i32 { if (B) { return 0; } return 1; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					if stderr, code := cli.exitOf(t, tc.source, target); code != 0 {
						t.Fatalf("exit %d, want 0\n%s", code, stderr)
					}
				})
			}
		})
	}
}
