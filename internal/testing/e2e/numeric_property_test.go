package e2e

import (
	"bytes"
	"fmt"
	"math/rand"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	e2eharness "github.com/jakechampion/lang/internal/testing/e2eharness"
)

type numType struct {
	name     string
	bits     int
	unsigned bool
	isFloat  bool
}

var intTypes = []numType{
	{"u8", 8, true, false},
	{"i32", 32, false, false}, {"u32", 32, true, false},
	{"i64", 64, false, false}, {"u64", 64, true, false},
}

var floatTypes = []numType{{"f32", 32, false, true}, {"f64", 64, false, true}}

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
		default:
			return []string{"0", "1", "18446744073709551615", "9223372036854775808", "9223372036854775807", "10000000000000000000", "5000000000"}
		}
	}
	switch t.bits {
	case 32:
		return []string{"0", "1", "0 - 1", "2147483647", "0 - 2147483647 - 1", "1000000", "0 - 1000000", "65536"}
	default:
		return []string{"0", "1", "0 - 1", "9223372036854775807", "0 - 9223372036854775807 - 1", "5000000000", "0 - 5000000000", "1000000000000"}
	}
}

const numImports = `import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";
import "std/f64";
`

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

func genNumProgram(r *rand.Rand) string {
	switch r.Intn(9) {
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
	case 7:
		return genFloatMath(r)
	default:
		return genIntToFloat(r)
	}
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.Intn(len(xs))] }

func genIntBinary(r *rand.Rand) string {
	t := pick(r, intTypes)
	lits := litsFor(t)
	op := pick(r, []string{"+", "-", "*", "&", "|", "^", "/", "%"})
	body := fmt.Sprintf("    let a: %s = %s;\n    let b: %s = %s;\n    %s\n",
		t.name, pick(r, lits), t.name, pick(r, lits), printInt(t, "a "+op+" b"))
	return wrapMain(body)
}

func genIntSaturating(r *rand.Rand) string {
	t := pick(r, intTypes)
	lits := litsFor(t)
	op := pick(r, []string{"+|", "-|", "*|"})
	body := fmt.Sprintf("    let a: %s = %s;\n    let b: %s = %s;\n    %s\n",
		t.name, pick(r, lits), t.name, pick(r, lits), printInt(t, "a "+op+" b"))
	return wrapMain(body)
}

func genIntShift(r *rand.Rand) string {
	t := pick(r, intTypes)
	a := pick(r, litsFor(t))
	op := pick(r, []string{"<<", ">>"})
	count := pick(r, []string{"0", "1", "7", "15", "31", "33", "63", "64", "65"})
	body := fmt.Sprintf("    let a: %s = %s;\n    let c: %s = %s;\n    %s\n",
		t.name, a, t.name, count, printInt(t, "a "+op+" c"))
	return wrapMain(body)
}

func genIntCompare(r *rand.Rand) string {
	t := pick(r, intTypes)
	lits := litsFor(t)
	op := pick(r, []string{"<", "<=", ">", ">=", "==", "!="})
	body := fmt.Sprintf("    let a: %s = %s;\n    let b: %s = %s;\n    print(pb(a %s b));\n",
		t.name, pick(r, lits), t.name, pick(r, lits), op)
	return wrapMain(body)
}

func genIntCast(r *rand.Rand) string {
	src := pick(r, intTypes)
	dst := pick(r, intTypes)
	body := fmt.Sprintf("    let a: %s = %s;\n    %s\n",
		src.name, pick(r, litsFor(src)), printInt(dst, "a as "+dst.name))
	return wrapMain(body)
}

func genFloatBinary(r *rand.Rand) string {
	t := pick(r, floatTypes)
	lits := litsFor(t)
	op := pick(r, []string{"+", "-", "*", "/"})
	body := fmt.Sprintf("    let a: %s = %s;\n    let b: %s = %s;\n    print((a %s b).to_string());\n",
		t.name, pick(r, lits), t.name, pick(r, lits), op)
	return wrapMain(body)
}

var floatMathExact = []string{"sqrt", "floor", "ceil", "trunc", "round", "abs"}

var floatMathLits = []string{
	"0.0", "0.0 - 0.0", "1.0", "0.0 - 1.0", "0.5", "0.0 - 0.5",
	"2.5", "0.0 - 2.5", "1.5", "0.0 - 1.5", "3.7", "0.0 - 3.7",
	"0.49999999999999994", "2.0", "0.25", "3.14159265358979",
	"4503599627370497.0", "16777217.0", "2147483648.0", "9.2e18",
	"1e-300", "1e10", "1e30", "0.0 - 1e30", "1e300",
	"1.0 / 0.0", "0.0 - 1.0 / 0.0", "0.0 / 0.0",
}

func genFloatMath(r *rand.Rand) string {
	m := pick(r, floatMathExact)
	body := fmt.Sprintf("    let a: f64 = %s;\n    print((a.%s()).to_string());\n",
		pick(r, floatMathLits), m)
	return wrapMain(body)
}

func genFloatToInt(r *rand.Rand) string {
	src := pick(r, floatTypes)
	dst := pick(r, intTypes)
	body := fmt.Sprintf("    let a: %s = %s;\n    %s\n",
		src.name, pick(r, litsFor(src)), printInt(dst, "a as "+dst.name))
	return wrapMain(body)
}

func genIntToFloat(r *rand.Rand) string {
	src := pick(r, intTypes)
	dst := pick(r, floatTypes)
	body := fmt.Sprintf("    let a: %s = %s;\n    print((a as %s).to_string());\n",
		src.name, pick(r, litsFor(src)), dst.name)
	return wrapMain(body)
}

func interpStdout(t *testing.T, src string) string {
	t.Helper()
	return interpStdoutGap(t, src, false)
}

func interpStdoutOrSkip(t *testing.T, src string) string {
	t.Helper()
	return interpStdoutGap(t, src, true)
}

func interpStdoutGap(t *testing.T, src string, skipOnGap bool) string {
	t.Helper()
	i := interpLoaded(t, src)
	var buf bytes.Buffer
	i.Stdout = &buf
	if _, err := i.CallByName("main", nil); err != nil {
		if skipOnGap {
			t.Skipf("interp coverage gap: %v\nsrc:\n%s", err, src)
		}
		t.Fatalf("interp coverage gap on a hand-written case: %v\nsrc:\n%s", err, src)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func trimOut(s string) string { return strings.TrimRight(strings.TrimSpace(s), "\n") }

func assertNumProgramAgrees(t *testing.T, src string) {
	t.Helper()
	assertNumProgramAgreesSkipping(t, src, nil, nil)
}

func assertGeneratedNumProgramAgrees(t *testing.T, src string, tally *legTally) {
	t.Helper()
	runBackendsAgainst(t, src, interpStdoutOrSkip(t, src), nil, tally)
}

func assertNumProgramAgreesSkipping(t *testing.T, src string, skip map[string]string, tally *legTally) {
	t.Helper()
	runBackendsAgainst(t, src, interpStdout(t, src), skip, tally)
}

var numBackendLegs = []backendLeg{
	{name: "x86_64", available: func() bool {
		_, _, ok := e2eharness.LookupX86_64Tooling()
		return ok
	}},
	{name: "arm64-linux", available: func() bool {
		_, _, ok := e2eharness.LookupArm64Tooling()
		return ok
	}},
	{name: "wasm32-wasi", available: func() bool {
		if runtime.GOOS == "windows" {
			return false
		}
		_, err := exec.LookPath("wasmtime")
		return err == nil
	}},
}

const numMinRunRatio = 0.98

func runBackendsAgainst(t *testing.T, src, want string, skip map[string]string, tally *legTally) {
	t.Helper()
	ran := func(name string) {
		if tally != nil {
			tally.legRan(name)
		}
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
		ran("x86_64")
		if got := trimOut(out); got != want {
			t.Errorf("x86_64 = %q, interp = %q\nsrc:\n%s", got, want, src)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if known(t, "arm64-linux") {
			return
		}
		out, _ := compileAndRunArm64(t, src)
		ran("arm64-linux")
		if got := trimOut(out); got != want {
			t.Errorf("arm64 = %q, interp = %q\nsrc:\n%s", got, want, src)
		}
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		if known(t, "wasm32-wasi") {
			return
		}
		comp := buildCLIComponent(t, src)
		ran("wasm32-wasi")
		out, stderr, ec := runWasmArtifact(t, comp, runOpts{})
		if ec != 0 {
			t.Fatalf("wasmtime exit=%d\nstdout:%s\nstderr:%s\nsrc:\n%s", ec, out, stderr, src)
		}
		if got := trimOut(out); got != want {
			t.Errorf("wasm = %q, interp = %q\nsrc:\n%s", got, want, src)
		}
	})
}

const minSeedsRunPct = 80

func TestNumericProperty_Differential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping property sweep in -short mode")
	}
	const seeds = 60
	ran := 0

	have, missing := availableLegs(numBackendLegs)
	if len(have) == 0 {
		t.Fatalf("no backend can run here (missing: %s) — every seed would compare "+
			"the interpreter against nothing and the sweep would still report PASS",
			describeLegs(missing))
	}
	t.Logf("backend legs available here: %s", describeLegs(have))
	tally := newLegTally(have, numMinRunRatio)
	t.Cleanup(func() { tally.check(t) })

	for s := 0; s < seeds; s++ {
		r := rand.New(rand.NewSource(int64(s)))
		src := genNumProgram(r)
		t.Run(fmt.Sprintf("seed%d", s), func(t *testing.T) {
			defer func() {
				if !t.Skipped() {
					ran++
					tally.seedCompared()
				}
			}()
			assertGeneratedNumProgramAgrees(t, src, tally)
		})
	}
	if ran*100 < seeds*minSeedsRunPct {
		t.Errorf("only %d of %d generated programs reached the interp oracle (floor %d%%) — "+
			"the sweep is mostly skips", ran, seeds, minSeedsRunPct)
	}
	t.Logf("%d of %d generated programs reached the oracle", ran, seeds)
}

func TestNumericProperty_FloatMathExact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	have, missing := availableLegs(numBackendLegs)
	if len(have) == 0 {
		t.Fatalf("no backend can run here (missing: %s) — every cell would compare "+
			"the interpreter against nothing and the sweep would still report PASS",
			describeLegs(missing))
	}
	t.Logf("backend legs available here: %s", describeLegs(have))
	tally := newLegTally(have, numMinRunRatio)
	t.Cleanup(func() { tally.check(t) })

	for _, m := range floatMathExact {
		for i, lit := range floatMathLits {
			t.Run(fmt.Sprintf("%s/lit%d", m, i), func(t *testing.T) {
				body := fmt.Sprintf("    let a: f64 = %s;\n    print((a.%s()).to_string());\n", lit, m)
				assertNumProgramAgreesSkipping(t, wrapMain(body), nil, tally)
				tally.seedCompared()
			})
		}
	}
}

func TestNumericProperty_Regressions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	cases := []struct {
		name, body string
	}{
		{"u8_add_wrap", `    let a: u8 = 255;
    print(((a + a) as i64).to_string());`},
		{"u32_mul_widen", `    let a: u32 = 4000000000;
    let b: u32 = 1;
    print(((a * b) as i64).to_string());`},
		{"f64_to_u8_wrap", `    let a: f64 = 3000000000.0;
    print(((a as u8) as i64).to_string());`},
		{"f64_to_i32_satpos", `    let a: f64 = 1e30;
    print((a as i32).to_string());`},
		{"f64_to_i64_nan", `    let a: f64 = 0.0 / 0.0;
    print((a as i64).to_string());`},
		{"u64max_to_f64", `    let a: u64 = 18446744073709551615;
    print((a as f64).to_string());`},
		{"u64max_to_f32", `    let a: u64 = 18446744073709551615;
    print((a as f32).to_string());`},
		{"u32_to_f32", `    let a: u32 = 4000000000;
    print((a as f32).to_string());`},
		{"i32_div_zero", `    let z: i32 = 0;
    let n: i32 = 10;
    print((n / z).to_string());`},
		{"i32_mod_zero", `    let z: i32 = 0;
    let n: i32 = 10;
    print((n % z).to_string());`},
		{"i32_min_div_neg1", `    let a: i32 = 0 - 2147483647 - 1;
    let b: i32 = 0 - 1;
    print((a / b).to_string());`},
		{"i32_min_mod_neg1", `    let a: i32 = 0 - 2147483647 - 1;
    let b: i32 = 0 - 1;
    print((a % b).to_string());`},
		{"i64_div_zero", `    let z: i64 = 0;
    let n: i64 = 100;
    print((n / z).to_string());`},
		{"i64_min_div_neg1", `    let a: i64 = 0 - 9223372036854775807 - 1;
    let b: i64 = 0 - 1;
    print((a / b).to_string());`},
		{"u32_div_zero", `    let z: u32 = 0;
    let n: u32 = 4000000000;
    print((n / z).to_string());`},
		{"u8_mod_zero", `    let z: u8 = 0;
    let n: u8 = 200;
    print(((n % z) as i64).to_string());`},
		{"i32_sat_add_hi", `    let a: i32 = 2147483647;
    let b: i32 = 1;
    print((a +| b).to_string());`},
		{"i32_sat_sub_lo", `    let a: i32 = 0 - 2147483647 - 1;
    let b: i32 = 1;
    print((a -| b).to_string());`},
		{"i32_sat_mul_min_neg1", `    let a: i32 = 0 - 2147483647 - 1;
    let b: i32 = 0 - 1;
    print((a *| b).to_string());`},
		{"i32_sat_mul_neg1_min", `    let a: i32 = 0 - 1;
    let b: i32 = 0 - 2147483647 - 1;
    print((a *| b).to_string());`},
		{"i64_sat_mul_min_neg1", `    let a: i64 = 0 - 9223372036854775807 - 1;
    let b: i64 = 0 - 1;
    print((a *| b).to_string());`},
		{"i64_sat_add_hi", `    let a: i64 = 9223372036854775807;
    let b: i64 = 1;
    print((a +| b).to_string());`},
		{"u64_sat_add_hi", `    let a: u64 = 18446744073709551615;
    let b: u64 = 1;
    print((a +| b).to_string());`},
		{"u64_sat_sub_lo", `    let a: u64 = 0;
    let b: u64 = 1;
    print((a -| b).to_string());`},
		{"u32_sat_mul_hi", `    let a: u32 = 4294967295;
    let b: u32 = 2;
    print(((a *| b) as i64).to_string());`},
		{"u8_sat_add_hi", `    let a: u8 = 255;
    let b: u8 = 1;
    print(((a +| b) as i64).to_string());`},
		{"u8_sat_sub_lo", `    let a: u8 = 0;
    let b: u8 = 1;
    print(((a -| b) as i64).to_string());`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertNumProgramAgrees(t, wrapMain(c.body+"\n"))
		})
	}
}

func FuzzNumericProperty(f *testing.F) {
	for s := int64(0); s < 16; s++ {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))
		assertGeneratedNumProgramAgrees(t, genNumProgram(r), nil)
	})
}
