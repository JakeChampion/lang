package e2eselfhost

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// interpFloatLitBatchSize is how many literals go into one generated program.
// The batch reports the 1-based index of its first mismatch as its exit status,
// so it has to stay under the 255 an exit code can carry.
const interpFloatLitBatchSize = 200

// floatShapedLit makes s lex as a float literal. A spelling with no `.`, `e` or
// `E` — every midpoint of two large doubles is a plain integer — would otherwise
// be an integer literal, and `f64_bits` of one is a type error.
func floatShapedLit(s string) string {
	if strings.ContainsAny(s, ".eE") {
		return s
	}
	return s + ".0"
}

// interpFloatLitCorpus is parseF64Corpus restricted to what can appear as a
// float literal in SOURCE: the native front end rejects an out-of-range
// spelling outright (P002, "invalid float literal"), so `1e309` and `1e-400`
// have no double to agree on and only the assemblers' runtime `.double`
// operands reach them.
func interpFloatLitCorpus() (lits []string, want []int64) {
	all, bits := parseF64Corpus()
	for i, s := range all {
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			continue
		}
		lits = append(lits, floatShapedLit(s))
		want = append(want, bits[i])
	}
	return lits, want
}

// interpFloatLitBatch builds one program comparing each literal's f64_bits
// against the strtod oracle's, returning the 1-based index of the first
// mismatch, or 0 when every literal agrees. The expected bits go in as decimal
// integer literals, which both engines read exactly.
func interpFloatLitBatch(lits []string, want []int64) string {
	var b strings.Builder
	b.WriteString("function main(): i32 {\n")
	for i, s := range lits {
		fmt.Fprintf(&b, "  if (f64_bits(%s) != %d) { return %d; }\n", s, want[i], i+1)
	}
	b.WriteString("  return 0;\n}\n")
	return b.String()
}

// TestSelfHostInterpFloatLiteralBits pins the self-host interpreter's decimal
// float literal reader against the native interpreter, bit for bit. The native
// interpreter is the differential oracle the whole self-host test strategy is
// measured against, so a literal the two engines read to different doubles
// makes every float-heavy differential comparison a comparison of two subtly
// different programs. It read literals by accumulating digits into an f64 and
// then multiplying or dividing by 10 once per exponent step, which compounds a
// rounding error: `1e30` came out 1 ULP low, `1e100` 3 ULP high, `1e-100` 6 ULP
// high and `8.98846567431158e307` 7 ULP low (#6824).
//
// Comparing f64_bits rather than a rendering keeps this a test of PARSING: the
// shortest-round-trip formatter prints whatever double it is handed, so a
// printing difference and a parsing difference show up in the same place.
//
// Host modes mirror TestSelfHostInterpU64: on Apple Silicon the driver is built
// for arm64-darwin through the in-process Mach-O path, off it with the Go
// x86-64 backend.
func TestSelfHostInterpFloatLiteralBits(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")

	var driverBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "interp_run.fern", "interp_run")
	}
	interpBin := buildLangBinForInterp(t)

	lits, want := interpFloatLitCorpus()
	if len(lits) < 500 {
		t.Fatalf("corpus collapsed to %d literals", len(lits))
	}
	for start := 0; start < len(lits); start += interpFloatLitBatchSize {
		end := start + interpFloatLitBatchSize
		if end > len(lits) {
			end = len(lits)
		}
		t.Run(fmt.Sprintf("batch-%d", start/interpFloatLitBatchSize), func(t *testing.T) {
			src := interpFloatLitBatch(lits[start:end], want[start:end])
			if got := interpExitStdin(t, interpBin, src, ""); got != 0 {
				t.Fatalf("native interp oracle exited %d — it disagrees with strtod on %q, so the corpus is wrong, not the self-host engine",
					got, lits[start+got-1])
			}
			if got := runDriverExit(t, runner, driverBin, []byte(src)); got != 0 {
				which := "?"
				if got >= 1 && start+got-1 < len(lits) {
					which = lits[start+got-1]
				}
				t.Errorf("self-host interp exited %d, want 0 — %q reads to different bits than native's %d",
					got, which, want[start+got-1])
			}
		})
	}
}
