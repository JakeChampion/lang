package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestPrintfBytesParity(t *testing.T) {
	var cases []invocation
	for _, tc := range e2eharness.PrintfByteCases() {
		cases = append(cases, invocation{name: tc.Name, args: tc.Args})
	}
	requireParity(t, "printf", cases)
	e2eharness.RunPrintfByteCases(t, fernBin(t, "printf"), crossPrefix(), nil)
}
