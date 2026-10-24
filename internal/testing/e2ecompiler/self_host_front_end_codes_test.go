package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

var frontEndCodeRE = regexp.MustCompile(`P\d{3}`)

func goFrontEndCodes(t *testing.T, src string) []string {
	t.Helper()
	if _, err := parser.Parse(src); err != nil {
		return uniqueSortedCodes(frontEndCodeRE.FindAllString(diag.Format("prog.fern", src, err), -1))
	}
	return nil
}

func TestSelfHostFrontEndNumericLiteralCodes(t *testing.T) {
	prog := func(lit string) string {
		return "function main(): i32 { let x: f64 = " + lit + "; return 0; }\n"
	}
	cases := []frontEndCodeCase{
		{"overflow-exponent", prog("1e309"), []string{"P002"}},
		{"overflow-far", prog("1e999"), []string{"P002"}},
		{"overflow-negated", prog("-1e309"), []string{"P002"}},
		{"overflow-fraction", prog("1.8e308"), []string{"P002"}},
		{"overflow-f32-suffix", prog("1e400f32"), []string{"P002"}},
		{"f32-range-not-checked-clean", prog("3.5e38"), nil},
		{"overflow-above-tie", prog("1.797693134862315808e308"), []string{"P002"}},
		{"below-tie-clean", prog("1.7976931348623158e308"), nil},
		{"max-finite-clean", prog("1.7976931348623157e308"), nil},
		{"underflow-clean", prog("1e-400"), nil},
		{"smallest-subnormal-clean", prog("5e-324"), nil},
		{"ordinary-float-clean", prog("1.5e2"), nil},
		{"overflow-in-unused-param-default",
			"function f(x: f64 = 1e309): i32 { return 0; }\nfunction main(): i32 { return 0; }\n",
			[]string{"P002"}},
		{"param-default-clean",
			"function f(x: f64 = 1.5): i32 { return 0; }\nfunction main(): i32 { return 0; }\n",
			nil},
	}
	runFrontEndCodeCases(t, cases)
}

type frontEndCodeCase struct {
	name string
	src  string
	want []string
}

func runFrontEndCodeCases(t *testing.T, cases []frontEndCodeCase) {
	t.Helper()
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../../compiler/drivers/checker_codes_run.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(interpBin, "-interp", driver)
			cmd.Stdin = strings.NewReader(tc.src)
			out, _ := cmd.Output()
			got := driverCodes(string(out))

			if !equalStrings(got, uniqueSortedCodes(tc.want)) {
				t.Errorf("self-host front-end codes = %v, want %v (src %s)", got, tc.want, tc.src)
			}
			if goCodes := goFrontEndCodes(t, tc.src); !equalStrings(got, goCodes) {
				t.Errorf("self-host codes %v disagree with the Go front end %v (src %s)", got, goCodes, tc.src)
			}
		})
	}
}

func TestSelfHostFrontEndTrailingCommas(t *testing.T) {
	runFrontEndCodeCases(t, []frontEndCodeCase{
		{"struct-type-parameters", "struct Pair[A, B,] { a: A, b: B }\nfunction main(): i32 { let p: Pair[i32, i32] = Pair { a: 1, b: 2 }; return p.a; }\n", nil},
		{"enum-type-parameters", "enum E[A, B,] { X(A), Y(B) }\nfunction main(): i32 { return 0; }\n", nil},
		{"type-arguments", "struct Pair[A, B] { a: A, b: B }\nfunction main(): i32 { let p: Pair[i32, i32,] = Pair { a: 1, b: 2 }; return p.a; }\n", nil},
		{"variant-payload", "enum E { X(i32, i32,), Y }\nfunction main(): i32 { let e: E = X(1, 2); return 0; }\n", nil},
	})
}
