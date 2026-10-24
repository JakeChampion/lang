package e2ecompiler

import (
	"os"
	"strings"
	"testing"

	e2eharness "github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestMain(m *testing.M) {
	if _, set := os.LookupEnv("FERN_IR_VERIFY"); !set {
		key, value, _ := strings.Cut(e2eharness.SelfHostVerify, "=")
		os.Setenv(key, value)
	}
	os.Exit(m.Run())
}
