package e2ecompiler

import (
	"os"
	"strings"
	"testing"

	e2eharness "github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestMain turns the self-host compiler's re-checks on for every child that
// inherits this process's environment; childEnv sets them for the rest.
func TestMain(m *testing.M) {
	if _, set := os.LookupEnv("FERN_IR_VERIFY"); !set {
		key, value, _ := strings.Cut(e2eharness.SelfHostVerify, "=")
		os.Setenv(key, value)
	}
	os.Exit(m.Run())
}
