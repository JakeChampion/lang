package arm64

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/fdlibm"
)

// transcendentalProgram touches all five helpers, so emitting it pulls in the
// whole bundle and its coefficient table.
const transcendentalProgram = `function main(): i32 {
    var r: f64 = __sin_f64(1.0) + __cos_f64(1.0) + __exp_f64(1.0) + __log_f64(2.0) + __pow_f64(2.0, 3.0);
    return r as i32;
}`

// Mach-O has no ".L" local-label prefix — retargetLocals rewrites them to "L"
// — so match either spelling.
var fcRefRe = regexp.MustCompile(`\.?Lfc_([A-Za-z0-9_]+)`)

// The kernels address the coefficient table by name, and the names come from
// fdlibm.Coeffs. A reference to a name the table does not carry is a
// mis-emitted load, so this checks every one resolves — cheaply, without
// running anything, since the e2e suites that would otherwise notice need
// qemu.
func TestTranscendentalLabelsResolve(t *testing.T) {
	for _, darwin := range []bool{false, true} {
		asm := compile(t, transcendentalProgram, Options{Darwin: darwin})
		known := map[string]bool{"tab": true, "2opi_bits": true}
		for _, c := range fdlibm.Coeffs {
			known[c.Name] = true
		}
		var bad []string
		for _, m := range fcRefRe.FindAllStringSubmatch(asm, -1) {
			if !known[m[1]] {
				bad = append(bad, m[1])
			}
		}
		if len(bad) > 0 {
			sort.Strings(bad)
			t.Errorf("darwin=%v: assembly references .Lfc_%s, which fdlibm.Coeffs does not carry",
				darwin, strings.Join(bad, ", .Lfc_"))
		}
		// A pattern that matched nothing would pass vacuously.
		if !strings.Contains(asm, "Lfc_tab:") {
			t.Errorf("darwin=%v: no coefficient table emitted for a program that calls all five helpers", darwin)
		}
	}
}
