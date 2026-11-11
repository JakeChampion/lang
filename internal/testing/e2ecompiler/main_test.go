package e2ecompiler

import (
	"os"
	"strings"
	"testing"

	e2eharness "github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--fern-budget-child" {
		os.Exit(budgetChild(os.Args[2:]))
	}
	if len(os.Args) > 2 && os.Args[1] == "--fern-budget-fixture" {
		os.Exit(budgetFixtureMain(os.Args[2]))
	}
	if _, set := os.LookupEnv("FERN_IR_VERIFY"); !set {
		key, value, _ := strings.Cut(e2eharness.SelfHostVerify, "=")
		os.Setenv(key, value)
	}
	os.Exit(m.Run())
}
