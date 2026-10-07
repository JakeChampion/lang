package interp

import (
	"bytes"
	"strings"
	"testing"
)

func TestREPLBannerNamesFern(t *testing.T) {
	var out bytes.Buffer
	if err := REPL(strings.NewReader(""), &out); err != nil {
		t.Fatalf("REPL: %v", err)
	}
	if !strings.HasPrefix(out.String(), "fern REPL") {
		t.Errorf("REPL banner = %q, want it to start with \"fern REPL\"", out.String())
	}
}
