package e2eselfhost

import (
	"os"
	"testing"
)

// Every compile here runs under FERN_SEM_IR_STRICT, so a module the typed
// path does not produce whole fails its test instead of silently keeping the
// AST lowering. A test that keeps the AST lowering on purpose clears the flag
// for its own compile, and setting it (even empty) in the environment
// overrides this default. childEnv strips every FERN_ key, so a compile built
// with it names the flag itself.
func TestMain(m *testing.M) {
	if _, set := os.LookupEnv("FERN_SEM_IR_STRICT"); !set {
		os.Setenv("FERN_SEM_IR_STRICT", "1")
	}
	os.Exit(m.Run())
}
