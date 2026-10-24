package printer

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

const costPerSourceByte = 128

const costGrowthRatio = 2.5

func costSource(fns int) string {
	var sb strings.Builder
	for i := 0; i < fns; i++ {
		sb.WriteString("function gen" + strconv.Itoa(i) + "(n: i32): i32 {\n")
		sb.WriteString("let acc: i32 = 0;\n")
		sb.WriteString("    let k: i32 = 1;\n")
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
