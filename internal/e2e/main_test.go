package e2e

import (
	"os"
	"testing"
)

// Every self-host compile here inherits FERN_SEM_IR_STRICT, so a module the
// typed path does not produce whole fails its test instead of silently keeping
// the AST lowering. Only the fern.fern CLI reads it, so it binds the tests that
// run the CLI on a codegen path: the emit drivers never reach the typed
// lowering, and `-check` returns before it. The semantic differential legs
// spell it off for their own compile, since a refusal there must fail as a
// mixed module rather than skip as a compile gap; setting it (even empty) in
// the environment overrides this default.
func TestMain(m *testing.M) {
	if _, set := os.LookupEnv("FERN_SEM_IR_STRICT"); !set {
		os.Setenv("FERN_SEM_IR_STRICT", "1")
	}
	os.Exit(m.Run())
}
