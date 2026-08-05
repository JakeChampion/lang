// Property-based differential testing for the numeric surface —
// the area (integer width / signedness / operators, float
// arithmetic, and float↔int conversions) that has produced the
// most cross-backend correctness bugs. A generator emits small,
// type-correct Fern programs that print the result of one numeric
// operation; each program runs through the interpreter (the
// oracle) and every available codegen backend (x86-64, arm64,
// wasm), and all outputs must agree.
//
// This complements the fernsmith diff-oracle (which generates
// whole control-flow programs but deliberately excludes floats and
// doesn't sweep the integer width/signedness matrix). Here the
// programs are tiny and targeted, so a disagreement points
// straight at a conversion / arithmetic / wrap bug.
//
// Division and modulo are generated only with operands that can't
// hit the two cross-backend-divergent edges (÷0 and INT_MIN/−1);
// those are a separate semantics decision, not a codegen bug.
package e2e

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// ---- numeric type model ----

type numType struct {
	name     string // fern spelling: "u8", "u32", "f64", ...
	bits     int    // 8/32/64
	unsigned bool
	isFloat  bool
}

var intTypes = []numType{
	{"u8", 8, true, false},
	{"i32", 32, false, false}, {"u32", 32, true, false},
	{"i64", 64, false, false}, {"u64", 64, true, false},
}

var floatTypes = []numType{{"f32", 32, false, true}, {"f64", 64, false, true}}

// litsFor returns a pool of literal expressions for a type — edge
// values (0, 1, −1, MIN, MAX, half-range) plus a couple of mid
// values. Signed negatives and the type minimums are spelled
// `0 - N (- 1)` because the lexer rejects a bare out-of-range
// negative literal (e.g. `-2147483648` for i32). The result
// strings are valid right-hand sides for `var x: T = …`.
func litsFor(t numType) []string {
	if t.isFloat {
		return []string{
			"0.0", "1.0", "0.0 - 1.0", "0.5", "3.14159",
			"1e10", "0.0 - 1e10", "1e30", "0.0 - 1e30",
			"16777217.0", "2147483648.0", "9.2e18",
		}
	}
	if t.unsigned {
		switch t.bits {
		case 8:
			return []string{"0", "1", "255", "128", "127", "200", "42"}
		case 32:
			return []string{"0", "1", "4294967295", "2147483648", "2147483647", "3000000000", "1000000"}
		default: // 64
			return []string{"0", "1", "18446744073709551615", "9223372036854775808", "9223372036854775807", "10000000000000000000", "5000000000"}
		}
	}
	switch t.bits {
	case 32:
		return []string{"0", "1", "0 - 1", "2147483647", "0 - 2147483647 - 1", "1000000", "0 - 1000000", "65536"}
	default: // 64
		return []string{"0", "1", "0 - 1", "9223372036854775807", "0 - 9223372036854775807 - 1", "5000000000", "0 - 5000000000", "1000000000000"}
	}
}

// imports every generated program needs: i64 for the result
// `.to_string()`, u64 for unsigned-64 printing, float for f-string,
// and the rest so the per-type receiver methods resolve.
const numImports = `import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";
import "std/float";
`

// printInt renders an integer-typed expression of type t as a
// decimal string. u64 prints through its own to_string (the full
// unsigned value); everything else casts to i64 first. The exact
// rendering doesn't matter — only that it's identical across
// backends — but u64 gets its own path so large values are legible
// in a failure.
func printInt(t numType, expr string) string {
	if t.unsigned && t.bits == 64 {
		return fmt.Sprintf("print((%s).to_string());", expr)
	}
	return fmt.Sprintf("print(((%s) as i64).to_string());", expr)
}

func wrapMain(body string) string {
	return numImports + "function pb(x: boolean): string { if (x) { return \"T\"; } return \"F\"; }\n" +
		"function main(): i32 {\n" + body + "    return 0;\n}\n"
}

// genNumProgram builds one random numeric-operation program.
func genNumProgram(r *rand.Rand) string {
	switch r.Intn(8) {
	case 0:
		return genIntBinary(r)
	case 1:
		return genIntShift(r)
	case 2:
		return genIntCompare(r)
	case 3:
		return genIntCast(r)
	case 4:
		return genFloatBinary(r)
	case 5:
		return genFloatToInt(r)
	case 6:
		return genIntSaturating(r)
	default:
		return genIntToFloat(r)
	}
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.Intn(len(xs))] }

func genIntBinary(r *rand.Rand) string {
	t := pick(r, intTypes)
	lits := litsFor(t)
	op := pick(r, []string{"+", "-", "*", "&", "|", "^", "/", "%"})
	// Division / modulo have a defined, never-trap contract now
	// (x/0 = 0, x%0 = x, INT_MIN/-1 = INT_MIN, INT_MIN%-1 = 0), so
	// the operands — including 0 and -1 divisors and the INT_MIN
	// dividend — are drawn from the full edge pool like every other
	// op. The pool already contains 0, 1, -1, and the type min/max.
	body := fmt.Sprintf("    var a: %s = %s;\n    var b: %s = %s;\n    %s\n",
		t.name, pick(r, lits), t.name, pick(r, lits), printInt(t, "a "+op+" b"))
	return wrapMain(body)
}

// genIntSaturating draws from the same edge-literal pool as genIntBinary but
// with the saturating operators (#5542), which clamp to the operand type's
// [MIN, MAX] instead of wrapping. The clamp bounds are per-width, so the whole
// width × signedness matrix is the interesting surface — exactly what this
// harness sweeps. `usize` isn't in intTypes, so the target-width rejection
// never fires here.
func genIntSaturating(r *rand.Rand) string {
	t := pick(r, intTypes)
	lits := litsFor(t)
	op := pick(r, []string{"+|", "-|", "*|"})
	body := fmt.Sprintf("    var a: %s = %s;\n    var b: %s = %s;\n    %s\n",
		t.name, pick(r, lits), t.name, pick(r, lits), printInt(t, "a "+op+" b"))
	return wrapMain(body)
}

func genIntShift(r *rand.Rand) string {
	t := pick(r, intTypes)
	a := pick(r, litsFor(t))
	op := pick(r, []string{"<<", ">>"})
	// Shift counts include the boundary / over-width values that
	// exercise the count-masking contract. Fern requires both shift
	// operands to share a type, so the count is typed as the
	// operand (every value below fits in the smallest type, u8).
	count := pick(r, []string{"0", "1", "7", "15", "31", "33", "63", "64", "65"})
	body := fmt.Sprintf("    var a: %s = %s;\n    var c: %s = %s;\n    %s\n",
		t.name, a, t.name, count, printInt(t, "a "+op+" c"))
	return wrapMain(body)
}

func genIntCompare(r *rand.Rand) string {
	t := pick(r, intTypes)
	lits := litsFor(t)
	op := pick(r, []string{"<", "<=", ">", ">=", "==", "!="})
	body := fmt.Sprintf("    var a: %s = %s;\n    var b: %s = %s;\n    print(pb(a %s b));\n",
		t.name, pick(r, lits), t.name, pick(r, lits), op)
	return wrapMain(body)
}

func genIntCast(r *rand.Rand) string {
	src := pick(r, intTypes)
	dst := pick(r, intTypes)
	body := fmt.Sprintf("    var a: %s = %s;\n    %s\n",
		src.name, pick(r, litsFor(src)), printInt(dst, "a as "+dst.name))
	return wrapMain(body)
}

func genFloatBinary(r *rand.Rand) string {
	t := pick(r, floatTypes)
	lits := litsFor(t)
	op := pick(r, []string{"+", "-", "*", "/"})
	body := fmt.Sprintf("    var a: %s = %s;\n    var b: %s = %s;\n    print((a %s b).to_string());\n",
		t.name, pick(r, lits), t.name, pick(r, lits), op)
	return wrapMain(body)
}

func genFloatToInt(r *rand.Rand) string {
	src := pick(r, floatTypes)
	dst := pick(r, intTypes)
	body := fmt.Sprintf("    var a: %s = %s;\n    %s\n",
		src.name, pick(r, litsFor(src)), printInt(dst, "a as "+dst.name))
	return wrapMain(body)
}

func genIntToFloat(r *rand.Rand) string {
	src := pick(r, intTypes)
	dst := pick(r, floatTypes)
	body := fmt.Sprintf("    var a: %s = %s;\n    print((a as %s).to_string());\n",
		src.name, pick(r, litsFor(src)), dst.name)
	return wrapMain(body)
}

// ---- oracle + comparison ----

// interpStdout runs `src` through the interpreter and returns what
// main() printed. A nil error with ok=false means an interp
// coverage gap (skip), matching the diff-oracle's policy of
// treating the interp as the source of truth where it runs.
func interpStdout(t *testing.T, src string) (string, bool) {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v\nsrc:\n%s", err, src)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v\nsrc:\n%s", err, src)
	}
	i := interp.New()
	var buf bytes.Buffer
	i.Stdout = &buf
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	if _, err := i.CallByName("main", nil); err != nil {
		return "", false
	}
	return strings.TrimRight(buf.String(), "\n"), true
}

func trimOut(s string) string { return strings.TrimRight(strings.TrimSpace(s), "\n") }

// assertNumProgramAgrees runs one generated program through every
// available backend and asserts they match the interp's output.
func assertNumProgramAgrees(t *testing.T, src string) {
	t.Helper()
	assertNumProgramAgreesSkipping(t, src, nil)
}

// assertNumProgramAgreesSkipping is assertNumProgramAgrees with a set of
// backends to skip, each mapped to the issue tracking why. A skipped
// backend reports the issue rather than passing silently, and the other
// backends still run — which is the point: that they agree is the
// evidence the skipped one has a bug rather than the program being
// invalid.
func assertNumProgramAgreesSkipping(t *testing.T, src string, skip map[string]string) {
	t.Helper()
	want, ok := interpStdout(t, src)
	if !ok {
		return
	}
	known := func(t *testing.T, backend string) bool {
		if issue, ok := skip[backend]; ok {
			t.Skipf("known divergence, see %s — remove this entry when it is fixed", issue)
			return true
		}
		return false
	}

	t.Run("x86_64", func(t *testing.T) {
		if known(t, "x86_64") {
			return
		}
		out, _ := compileAndRunX86_64(t, src)
		if got := trimOut(out); got != want {
			t.Errorf("x86_64 = %q, interp = %q\nsrc:\n%s", got, want, src)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if known(t, "arm64") {
			return
		}
		out, _ := compileAndRunArm64(t, src)
		if got := trimOut(out); got != want {
			t.Errorf("arm64 = %q, interp = %q\nsrc:\n%s", got, want, src)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if known(t, "wasm") {
			return
		}
		comp := buildNumComponent(t, src)
		out, stderr, ec := runComponent(t, comp, runOpts{})
		if ec != 0 {
			t.Fatalf("wasmtime exit=%d\nstdout:%s\nstderr:%s\nsrc:\n%s", ec, out, stderr, src)
		}
		if got := trimOut(out); got != want {
			t.Errorf("wasm = %q, interp = %q\nsrc:\n%s", got, want, src)
		}
	})
}

// buildNumComponent builds a plain wasi:cli/run component the way
// `fern -target wasm` does — no result-printer wrapper, so the
// program's own `print`s are the only stdout (the oracle's
// buildComponent appends main's return value, which would show up
// as a spurious trailing "0" line here).
func buildNumComponent(t *testing.T, src string) string {
	t.Helper()
	skipIfPreview2Missing(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	bin, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		CliRunResult:       true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	return finishComponentFromCoreBytes(t, bin)
}

// TestNumericProperty_Differential is the deterministic, seeded
// sweep — fast enough for `go test ./...`, with the fuzz target
// below for deeper search. Each seed is its own sub-test so a
// failure names the exact program.
func TestNumericProperty_Differential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping property sweep in -short mode")
	}
	const seeds = 60
	for s := 0; s < seeds; s++ {
		r := rand.New(rand.NewSource(int64(s)))
		src := genNumProgram(r)
		t.Run(fmt.Sprintf("seed%d", s), func(t *testing.T) {
			assertNumProgramAgrees(t, src)
		})
	}
}

// TestNumericProperty_Regressions pins the specific programs the
// generator + fuzzer surfaced, so each stays covered deterministically
// regardless of how the random generator evolves. Each was a real
// cross-backend divergence before the fixes in this change.
func TestNumericProperty_Regressions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	cases := []struct {
		name, body string
	}{
		// Sub-i32 arithmetic wraps to the type width (interp used
		// not to narrow).
		{"u8_add_wrap", `    var a: u8 = 255;
    print(((a + a) as i64).to_string());`},
		// u32 widening to i64 zero-extends (interp stored u32
		// sign-extended, so a high-bit value widened negative).
		{"u32_mul_widen", `    var a: u32 = 4000000000;
    var b: u32 = 1;
    print(((a * b) as i64).to_string());`},
		// float→unsigned-sub-i32 must narrow to the dest width.
		{"f64_to_u8_wrap", `    var a: f64 = 3000000000.0;
    print(((a as u8) as i64).to_string());`},
		// float→int saturates (out of range / NaN).
		{"f64_to_i32_satpos", `    var a: f64 = 1e30;
    print((a as i32).to_string());`},
		{"f64_to_i64_nan", `    var a: f64 = 0.0 / 0.0;
    print((a as i64).to_string());`},
		// unsigned→float converts from the unsigned magnitude
		// (interp treated u64 max as signed -1; arm lacked ucvtf).
		{"u64max_to_f64", `    var a: u64 = 18446744073709551615;
    print((a as f64).to_string());`},
		{"u64max_to_f32", `    var a: u64 = 18446744073709551615;
    print((a as f32).to_string());`},
		{"u32_to_f32", `    var a: u32 = 4000000000;
    print((a as f32).to_string());`},
		// Integer division never traps (well-defined contract):
		// x/0 = 0, x%0 = x, INT_MIN/-1 = INT_MIN, INT_MIN%-1 = 0.
		{"i32_div_zero", `    var z: i32 = 0;
    var n: i32 = 10;
    print((n / z).to_string());`},
		{"i32_mod_zero", `    var z: i32 = 0;
    var n: i32 = 10;
    print((n % z).to_string());`},
		{"i32_min_div_neg1", `    var a: i32 = 0 - 2147483647 - 1;
    var b: i32 = 0 - 1;
    print((a / b).to_string());`},
		{"i32_min_mod_neg1", `    var a: i32 = 0 - 2147483647 - 1;
    var b: i32 = 0 - 1;
    print((a % b).to_string());`},
		{"i64_div_zero", `    var z: i64 = 0;
    var n: i64 = 100;
    print((n / z).to_string());`},
		{"i64_min_div_neg1", `    var a: i64 = 0 - 9223372036854775807 - 1;
    var b: i64 = 0 - 1;
    print((a / b).to_string());`},
		{"u32_div_zero", `    var z: u32 = 0;
    var n: u32 = 4000000000;
    print((n / z).to_string());`},
		{"u8_mod_zero", `    var z: u8 = 0;
    var n: u8 = 200;
    print(((n % z) as i64).to_string());`},
		// Saturating arithmetic (#5542) clamps at the operand width.
		// The edges are the two clamp directions per signedness, plus
		// the signed-mul `MIN / -1` pair the division round-trip would
		// otherwise read as non-overflowing.
		{"i32_sat_add_hi", `    var a: i32 = 2147483647;
    var b: i32 = 1;
    print((a +| b).to_string());`},
		{"i32_sat_sub_lo", `    var a: i32 = 0 - 2147483647 - 1;
    var b: i32 = 1;
    print((a -| b).to_string());`},
		{"i32_sat_mul_min_neg1", `    var a: i32 = 0 - 2147483647 - 1;
    var b: i32 = 0 - 1;
    print((a *| b).to_string());`},
		{"i32_sat_mul_neg1_min", `    var a: i32 = 0 - 1;
    var b: i32 = 0 - 2147483647 - 1;
    print((a *| b).to_string());`},
		{"i64_sat_mul_min_neg1", `    var a: i64 = 0 - 9223372036854775807 - 1;
    var b: i64 = 0 - 1;
    print((a *| b).to_string());`},
		{"i64_sat_add_hi", `    var a: i64 = 9223372036854775807;
    var b: i64 = 1;
    print((a +| b).to_string());`},
		{"u64_sat_add_hi", `    var a: u64 = 18446744073709551615;
    var b: u64 = 1;
    print((a +| b).to_string());`},
		{"u64_sat_sub_lo", `    var a: u64 = 0;
    var b: u64 = 1;
    print((a -| b).to_string());`},
		{"u32_sat_mul_hi", `    var a: u32 = 4294967295;
    var b: u32 = 2;
    print(((a *| b) as i64).to_string());`},
		{"u8_sat_add_hi", `    var a: u8 = 255;
    var b: u8 = 1;
    print(((a +| b) as i64).to_string());`},
		{"u8_sat_sub_lo", `    var a: u8 = 0;
    var b: u8 = 1;
    print(((a -| b) as i64).to_string());`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertNumProgramAgrees(t, wrapMain(c.body+"\n"))
		})
	}
}

// FuzzNumericProperty drives the same generator from fuzz-provided
// entropy. Run with:
//
//	go test -run=^$ -fuzz=FuzzNumericProperty ./internal/e2e
func FuzzNumericProperty(f *testing.F) {
	for s := int64(0); s < 16; s++ {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))
		assertNumProgramAgrees(t, genNumProgram(r))
	})
}
