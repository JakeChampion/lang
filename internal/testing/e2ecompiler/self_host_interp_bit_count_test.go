package e2ecompiler

import (
	"fmt"
	"math/bits"
	"runtime"
	"strings"
	"testing"
)

// Pin every bit position, zero, complements and mixed patterns against Go's
// integer oracle, and __mulhi_u64 against bits.Mul64. In particular, u64
// values must retain their high 32 bits.
func TestSelfHostInterpBitCounts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")
	var driver string
	var runner []string
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		driver = buildSelfHostBinArm64Darwin(t, dir, "drivers/interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driver = buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
	}
	oracle := buildLangBinForInterp(t)
	values := []uint64{0, ^uint64(0), 0xaaaaaaaaaaaaaaaa, 0x5555555555555555, 0x0123456789abcdef}
	for bit := range 64 {
		values = append(values, uint64(1)<<bit, ^(uint64(1) << bit))
	}
	for start := 0; start < len(values); start += 16 {
		end := min(start+16, len(values))
		t.Run(fmt.Sprintf("batch-%d", start/16), func(t *testing.T) {
			var src strings.Builder
			src.WriteString("function main(): i32 {\n")
			for i, value := range values[start:end] {
				fmt.Fprintf(&src, "let x%d: u64 = %du64;\n", i, value)
				checks := []struct {
					name, cast string
					want       int
				}{
					{"__clz32", "u32", bits.LeadingZeros32(uint32(value))},
					{"__ctz32", "u32", bits.TrailingZeros32(uint32(value))},
					{"__popcount32", "u32", bits.OnesCount32(uint32(value))},
					{"__clz64", "u64", bits.LeadingZeros64(value)},
					{"__ctz64", "u64", bits.TrailingZeros64(value)},
					{"__popcount64", "u64", bits.OnesCount64(value)},
				}
				for j, check := range checks {
					fmt.Fprintf(&src, "if (%s(x%d as %s) != %d) { return %d; }\n", check.name, i, check.cast, check.want, i*len(checks)+j+1)
				}
				hi, _ := bits.Mul64(value, 0xa2f9836e4e441529)
				fmt.Fprintf(&src, "if (__mulhi_u64(x%d, 11743562013128004905u64) != %du64) { return %d; }\n", i, hi, 100+i)
			}
			src.WriteString("return 0;\n}\n")
			if got := interpExitStdin(t, oracle, src.String(), ""); got != 0 {
				t.Fatalf("Go interpreter exited %d, want 0\n%s", got, src.String())
			}
			if got := runDriverExit(t, runner, driver, []byte(src.String())); got != 0 {
				t.Fatalf("self-host interpreter exited %d, want 0\n%s", got, src.String())
			}
		})
	}
}
