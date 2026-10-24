package diag

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var emittedCodeRE = regexp.MustCompile(`(?:err\w*Code\(|ErrCode:\s*|\bCode:\s*)[^"]*"([EP][0-9]{3})"`)

var fernEmittedCodeRE = regexp.MustCompile(`(?:\(|:)\s*"([EP][0-9]{3})"`)

func TestEmittedCodesHaveExplanations(t *testing.T) {
	dirs := []string{"../../check/checker", "../parser", "../../pkg/modload", "../../pkg/platforms", "../../check/defaultargs", "../../../cmd/fern"}
	emitted := map[string]string{}
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
	fern, err := filepath.Glob("../../../compiler/*.fern")
	if err != nil || len(fern) == 0 {
		t.Fatalf("glob compiler/*.fern: %v (%d files)", err, len(fern))
	}
	for _, f := range fern {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range fernEmittedCodeRE.FindAllStringSubmatch(string(b), -1) {
			if _, seen := emitted[m[1]]; !seen {
				emitted[m[1]] = f
			}
		}
	}
	if len(emitted) == 0 {
		t.Fatal("scraped zero emitted codes — the emission-call regex or the source paths broke")
	}
	for code, where := range emitted {
		if Explain(code) == "" {
			t.Errorf("code %s is emitted (%s) but has no explanation — add internal/syntax/diag/explanations/%s.md", code, where, code)
		}
	}
}
