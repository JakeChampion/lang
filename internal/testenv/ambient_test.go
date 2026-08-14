package testenv

import "testing"

// The repo-wide net: `go test ./...` fails here when the shell that started it
// exports a Semantic variable. The suites that also compile in-process wire
// MustCheckAmbient into their own TestMain, so a targeted run of one of those
// packages refuses to start rather than reporting a result about a compiler
// configuration nobody chose.
func TestAmbientEnvironmentIsClean(t *testing.T) {
	if err := CheckAmbient(); err != nil {
		t.Fatal(err)
	}
}
