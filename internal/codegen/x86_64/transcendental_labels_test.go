package x86_64

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

var fcRefRe = regexp.MustCompile(`\.Lfc_([A-Za-z0-9_]+)`)

// Every .Lfc_* the kernels load has to be a label the coefficient table
// emitted, and the table is fdlibm.Coeffs. Checked here rather than left to
// the e2e suites so a stale name fails in milliseconds.
func TestTranscendentalLabelsResolve(t *testing.T) {
	asm := compile(t, transcendentalProgram)
	known := map[string]bool{"2opi_bits": true}
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
		t.Errorf("assembly references .Lfc_%s, which fdlibm.Coeffs does not carry", strings.Join(bad, ", .Lfc_"))
	}
	for _, c := range fdlibm.Coeffs {
		if !strings.Contains(asm, ".Lfc_"+c.Name+":") {
			t.Errorf("the coefficient table does not define .Lfc_%s", c.Name)
		}
	}
}
