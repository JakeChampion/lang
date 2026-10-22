package checker

import (
	"strings"
	"testing"
)

// A method named main is not the entry point, so E084 leaves it alone even
// when E021 refuses its receiver and the receiver is hoisted into its
// parameters.
func TestMainSignatureSkipsMethods(t *testing.T) {
	err, _ := checkFiles(t, map[string]string{
		"main.fern": "function (p: Nope) main(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n",
	}, "main.fern")
	if err == nil {
		t.Fatal("a method on an unknown receiver type checked clean, want E021")
	}
	if !strings.Contains(err.Error(), "method receiver type") {
		t.Errorf("error %q is not the E021 receiver refusal", err)
	}
	if strings.Contains(err.Error(), "main takes no parameters") {
		t.Errorf("error %q reports E084 for a method", err)
	}
}
