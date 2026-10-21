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
	if len(rows) == 0 || strings.Contains(string(report), "refused") {
		t.Fatalf("fixture must use semantic ownership: %s", report)
	}
	// A refused declaration or instance exits the compile (semlower), so a
	// fixture that reaches here fell back on nothing. Reachability drops the
	// dead instances of a module (a plain server's unbuilt TLS generics, say),
	// so produced instances may be fewer than created; what must hold is that
	// every declaration the module keeps lowered through the typed pipeline.
	for _, row := range rows {
		if row[1] == "0" || row[1] != row[2] {
			t.Fatalf("fixture partially fell back: %s", report)
		}
	}
}
