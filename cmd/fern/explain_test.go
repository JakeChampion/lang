package main

import (
	"bytes"
	"strings"
	"testing"
)

// `fern -explain ""` lists the codes, as its help text says; a known code
// prints its explanation and an unknown one fails with the list.
func TestRunExplain(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runExplain("", &out, &errOut); code != 0 || !strings.Contains(out.String(), "E048") {
		t.Errorf("runExplain(\"\") = %d, stdout %q; want 0 and a list naming E048", code, out.String())
	}
	out.Reset()
	if code := runExplain("E048", &out, &errOut); code != 0 || !strings.Contains(out.String(), "E048") {
		t.Errorf("runExplain(E048) = %d, stdout %q", code, out.String())
	}
	out.Reset()
	if code := runExplain("E999999", &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "unknown error code") {
		t.Errorf("runExplain(E999999) = %d, stderr %q; want 1 and an unknown-code message", code, errOut.String())
	}
}
