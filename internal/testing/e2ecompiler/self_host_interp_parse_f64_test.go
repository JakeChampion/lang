package e2ecompiler

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const interpFloatLitBatchSize = 200

func floatShapedLit(s string) string {
	if strings.ContainsAny(s, ".eE") {
		return s
	}
	return s + ".0"
}

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

func interpFloatLitBatch(lits []string, want []int64) string {
	var b strings.Builder
	b.WriteString("function main(): i32 {\n")
	for i, s := range lits {
		fmt.Fprintf(&b, "  if (f64_bits(%s) != %d) { return %d; }\n", s, want[i], i+1)
	}
	b.WriteString("  return 0;\n}\n")
	return b.String()
}

func TestSelfHostInterpFloatLiteralBits(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")

	var driverBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "drivers/interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
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
