package e2eharness

import (
	"regexp"
	"strings"
	"testing"
)

// RequireCompleteSemanticLowering prevents a production-compiler test from
// silently exercising AST fallback instead of the typed ownership pipeline.
func RequireCompleteSemanticLowering(t *testing.T, report []byte) {
	t.Helper()
	rows := regexp.MustCompile(`FERN_SEM_IR: module: produced ([0-9]+) of ([0-9]+) declarations and ([0-9]+) of ([0-9]+) instances`).FindAllStringSubmatch(string(report), -1)
	if len(rows) == 0 || strings.Contains(string(report), "refused") || strings.Contains(string(report), "the AST lowering stands") {
		t.Fatalf("fixture must use semantic ownership: %s", report)
	}
	for _, row := range rows {
		if row[1] == "0" || row[1] != row[2] || row[3] != row[4] {
			t.Fatalf("fixture partially fell back: %s", report)
		}
	}
}
