package diag

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// emittedCodeRE captures the stable code passed to a diagnostic-emission
// call. It matches the `err…Code(pos, "CODE", …)` family (checker's
// errfCode, parser's errorfCode — both end in `Code(`) and the
// `ErrCode: "CODE"` / `Code: "CODE"` struct-field form. `[^"]*` skips the
// position argument (which never contains a quote) so a `)` inside it,
// e.g. `errfCode(body[len(b)-1].P, "E052", …)`, doesn't defeat the match.
// Anchored on the `Code`-suffixed call/field so a bare code mention in a
// comment or message string is never picked up.
var emittedCodeRE = regexp.MustCompile(`(?:err\w*Code\(|ErrCode:\s*|\bCode:\s*)[^"]*"([EP][0-9]{3})"`)

// TestEmittedCodesHaveExplanations is the catalogue-completeness gate
// (#4413 Rec §4): every diagnostic code the checker / parser / modload
// actually EMITS must have a `fern explain`-able explanation under
// internal/diag/explanations/, so `fern -explain <code>` never bottoms out
// on "unknown code" for a diagnostic a user can hit. It scrapes the
// emission sites from source (rather than a hand-maintained list, which
// drifts — the older TestAvailableCodesEnumeratesCatalogue pins a fixed
// subset) so a newly-added code with no explanation fails here. This test
// added E065 (the `str`-escape sibling of E063), which was emitted with no
// explanation.
func TestEmittedCodesHaveExplanations(t *testing.T) {
	// Test runs in the package dir (internal/diag); the emitters are its
	// siblings. modload/parser share the same emission helpers as checker;
	// cmd/fern emits E066 (capability enforcement) via checker.Error, and
	// internal/platforms owns that pass — scan both so new codes there
	// can't ship without explanations either.
	dirs := []string{"../checker", "../parser", "../modload", "../platforms", "../../cmd/fern"}
	emitted := map[string]string{} // code -> first source file it's emitted from
	for _, d := range dirs {
		files, err := filepath.Glob(filepath.Join(d, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", d, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			for _, m := range emittedCodeRE.FindAllStringSubmatch(string(b), -1) {
				if _, seen := emitted[m[1]]; !seen {
					emitted[m[1]] = f
				}
			}
		}
	}
	if len(emitted) == 0 {
		t.Fatal("scraped zero emitted codes — the emission-call regex or the source paths broke")
	}
	for code, where := range emitted {
		if Explain(code) == "" {
			t.Errorf("code %s is emitted (%s) but has no explanation — add internal/diag/explanations/%s.md", code, where, code)
		}
	}
}
