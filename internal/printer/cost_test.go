package printer

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// A cost ceiling on `fern -fmt -d`. ccd2c82 made the alignment linear in space
// (#8526); what #8586 then found is that nothing HOLDS it there. The O(m*n)
// table it replaced wanted 48 GB for the largest source in this tree, and the
// formatter was OOM-killed rather than failing. Correctness tests do not see
// that — the table is built whatever the content is — and a gate that only
// bounds the output proves nothing about the cost of producing it.
//
// Both halves matter. The ratio is what separates a large constant from a
// quadratic: the table implementation allocates 3,965 bytes per source byte at
// 6,500 lines and 7,890 at 13,000, so it fails the ratio even at a size that
// still fits in RAM. The absolute ceiling is what keeps a regression from
// reaching the size where the machine dies before the assertion runs.

// costPerSourceByte bounds Format + UnifiedDiff against the size of their
// input. The linear-space alignment allocates about 33x on this source; the
// ceiling leaves room for a constant-factor change without leaving room for a
// return of the table.
const costPerSourceByte = 128

// costGrowthRatio bounds the work at 2N against the work at N. Linear cost
// lands at 2.0 (measured 2.03 / 2.07 across the sizes below, 2026-09-06);
// anything quadratic in file size lands at 4.0.
const costGrowthRatio = 2.5

// costSource generates `fns` copies of a small function, written with no
// consistent indentation, so formatting changes almost every line and the diff
// has to align a whole file rather than a hunk. The statement vocabulary is
// deliberately tiny and repeated across functions: every line the formatter
// emits also occurs somewhere in the input, so the alignment cannot be
// short-circuited by discarding lines that appear on one side only.
func costSource(fns int) string {
	var sb strings.Builder
	for i := 0; i < fns; i++ {
		sb.WriteString("function gen" + strconv.Itoa(i) + "(n: i32): i32 {\n")
		sb.WriteString("var acc: i32 = 0;\n")
		sb.WriteString("    var k: i32 = 1;\n")
		sb.WriteString("while (k < n) {\n")
		sb.WriteString("        if (k > 3) {\n")
		sb.WriteString("acc = acc + k;\n")
		sb.WriteString("} else {\n")
		sb.WriteString("            acc = acc + 1;\n")
		sb.WriteString("}\n")
		sb.WriteString("k = k + 1;\n")
		sb.WriteString("    }\n")
		sb.WriteString("return acc;\n")
		sb.WriteString("}\n")
	}
	return sb.String()
}

// fmtDiffAlloc returns the bytes Format plus UnifiedDiff allocate for src —
// the printer's half of `fern -fmt -d`. Parsing is deliberately outside the
// measured window: the AST is the parser's cost, not this package's.
func fmtDiffAlloc(t *testing.T, src string) uint64 {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("generated source does not parse: %v", err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	formatted := Format(prog)
	diff := UnifiedDiff(src, formatted, "big.fern", "big.fern")
	runtime.ReadMemStats(&after)
	if diff == "" {
		t.Fatal("generated source formats to itself — the diff half is not being measured")
	}
	runtime.KeepAlive(formatted)
	runtime.KeepAlive(diff)
	return after.TotalAlloc - before.TotalAlloc
}

func TestFormatAndDiffCostStaysLinearInFileSize(t *testing.T) {
	const smallFns = 1000
	small := costSource(smallFns)
	large := costSource(2 * smallFns)

	smallAlloc := fmtDiffAlloc(t, small)
	largeAlloc := fmtDiffAlloc(t, large)

	for _, c := range []struct {
		name  string
		src   string
		alloc uint64
	}{{"N", small, smallAlloc}, {"2N", large, largeAlloc}} {
		if per := float64(c.alloc) / float64(len(c.src)); per > costPerSourceByte {
			t.Errorf("%s (%d lines, %d bytes): Format + UnifiedDiff allocated %d bytes, %.0fx the source; ceiling is %dx",
				c.name, strings.Count(c.src, "\n"), len(c.src), c.alloc, per, costPerSourceByte)
		}
	}
	if got := float64(largeAlloc) / float64(smallAlloc); got > costGrowthRatio {
		t.Errorf("doubling the source multiplied the work by %.2f (%d -> %d bytes); ceiling is %.2f — the cost is not linear in file size",
			got, smallAlloc, largeAlloc, costGrowthRatio)
	}
}
