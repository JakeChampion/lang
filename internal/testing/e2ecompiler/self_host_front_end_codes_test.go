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

// frontEndCodeRE pulls the front end's P0NN codes out of a formatted
// diagnostic. The checker's E0NN codes have their own gate next door.
var frontEndCodeRE = regexp.MustCompile(`P\d{3}`)

// goFrontEndCodes returns the sorted, de-duplicated code set the Go FRONT END
// reports for src — empty when the source parses.
func goFrontEndCodes(t *testing.T, src string) []string {
	t.Helper()
	if _, err := parser.Parse(src); err != nil {
		return uniqueSortedCodes(frontEndCodeRE.FindAllString(diag.Format("prog.fern", src, err), -1))
	}
	return nil
}

// TestSelfHostFrontEndNumericLiteralCodes is the FRONT-END half of the
// self-host / native diagnostic differential: the codes reported before the
// checker ever runs. It drives compiler/drivers/checker_codes_run.fern with
// the native interpreter — no cross toolchain, so it runs on every host — and
// asserts the self-host code set matches what the Go front end reports for the
// same source.
//
// The E-code gate next door is structurally blind to this class: its oracle is
// the Go CHECKER, so a program the Go PARSER rejects reaches it as "no codes"
// and any front-end divergence reads as agreement. That is how #6842 hid. The
// self-host front end range-checked no float literal, so `1e309` became +Inf
// and compiled where native reported P002 — the two engines disagreed on which
// programs the language accepts, with every existing gate green.
//
// The accepted rows matter as much as the rejected ones: the boundary is
// decided by round-to-nearest, so a check that is one ULP too eager rejects
// the largest finite double, and UNDERFLOW is not a range error at all
// (strconv returns a subnormal / ±0 with no error there).
func TestSelfHostFrontEndNumericLiteralCodes(t *testing.T) {
	// prog wraps a float literal in the smallest program that binds it.
	prog := func(lit string) string {
		return "function main(): i32 { let x: f64 = " + lit + "; return 0; }\n"
	}
	cases := []frontEndCodeCase{
		{"overflow-exponent", prog("1e309"), []string{"P002"}},
		{"overflow-far", prog("1e999"), []string{"P002"}},
		// The sign is a separate unary-minus token, so the literal the front
		// end range-checks is the unsigned spelling either way.
		{"overflow-negated", prog("-1e309"), []string{"P002"}},
		{"overflow-fraction", prog("1.8e308"), []string{"P002"}},
		// An f32 suffix does not narrow the check: native parses every float
		// literal at f64 width and only the f64 range gates it, so `3.5e38`
		// (out of f32's range) is a valid literal and `1e400f32` is not.
		{"overflow-f32-suffix", prog("1e400f32"), []string{"P002"}},
		{"f32-range-not-checked-clean", prog("3.5e38"), nil},
		// Rounding boundary: the tie is 2^1024 - 2^970, which rounds to +Inf
		// (ties-to-even), while the spelling just under it is the largest
		// finite double.
		{"overflow-above-tie", prog("1.797693134862315808e308"), []string{"P002"}},
		{"below-tie-clean", prog("1.7976931348623158e308"), nil},
		{"max-finite-clean", prog("1.7976931348623157e308"), nil},
		// Underflow is accepted by both engines, subnormals included.
		{"underflow-clean", prog("1e-400"), nil},
		{"smallest-subnormal-clean", prog("5e-324"), nil},
		{"ordinary-float-clean", prog("1.5e2"), nil},
		// A parameter DEFAULT is the one position nothing evaluates unless a
		// call site omits the argument, so it sits outside the checker's body
		// walk. Native rejects the literal at parse time either way.
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

// runFrontEndCodeCases asserts each case's self-host code set is both the
// table's and the Go front end's.
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

// TestSelfHostFrontEndTrailingCommas: a trailing comma is legal in every list
// (internal/syntax/parser/trailing_comma_test.go), including the three the two
// front ends disagreed on (#11851).
func TestSelfHostFrontEndTrailingCommas(t *testing.T) {
	runFrontEndCodeCases(t, []frontEndCodeCase{
		{"struct-type-parameters", "struct Pair[A, B,] { a: A, b: B }\nfunction main(): i32 { let p: Pair[i32, i32] = Pair { a: 1, b: 2 }; return p.a; }\n", nil},
		{"enum-type-parameters", "enum E[A, B,] { X(A), Y(B) }\nfunction main(): i32 { return 0; }\n", nil},
		{"type-arguments", "struct Pair[A, B] { a: A, b: B }\nfunction main(): i32 { let p: Pair[i32, i32,] = Pair { a: 1, b: 2 }; return p.a; }\n", nil},
		{"variant-payload", "enum E { X(i32, i32,), Y }\nfunction main(): i32 { let e: E = X(1, 2); return 0; }\n", nil},
	})
}
