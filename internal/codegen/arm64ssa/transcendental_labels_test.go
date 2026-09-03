package arm64ssa

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/fdlibm"
)

var fcRefRe = regexp.MustCompile(`\.Lfc_([A-Za-z0-9_]+)`)

// The transcendental helper bodies address the coefficient table by label, and
// the labels are fdlibm.Coeffs' names. A body loading a name the table does
// not carry assembles to an undefined symbol, which nothing here would notice
// until a link — so check it directly, and check the reverse too: an entry no
// body loads is dead weight in five emitters.
func TestTranscendentalLabelsResolve(t *testing.T) {
	var b strings.Builder
	b.WriteString(renderHelper(emitTranscendentalRodata))
	for name := range transcendentalHelpers {
		b.WriteString(renderHelper(runtimeHelperEmitters[name]))
	}
	b.WriteString(renderHelper(runtimeHelperEmitters["__rem_pio2_large"]))
	asm := b.String()

	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\.Lfc_([A-Za-z0-9_]+):`).FindAllStringSubmatch(asm, -1) {
		defined[m[1]] = true
	}
	if len(defined) == 0 {
		t.Fatal("no .Lfc_* labels defined — the coefficient table did not emit, which would make this test vacuous")
	}
	var bad []string
	for _, m := range fcRefRe.FindAllStringSubmatch(asm, -1) {
		if !defined[m[1]] {
			bad = append(bad, m[1])
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf("helper bodies reference .Lfc_%s, which the table does not define", strings.Join(bad, ", .Lfc_"))
	}
	for _, c := range fdlibm.Coeffs {
		if !defined[c.Name] {
			t.Errorf("the table does not define .Lfc_%s", c.Name)
		}
	}
}
