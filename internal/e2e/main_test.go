package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testenv"
)

// This package compiles in process, and internal/ast reads its compile-mode
// flags from the environment at init: an exported FERN_SANITIZE=1 changes every
// compile here, and a child started with cmd.Env == nil inherits the rest.
// Refusing to run beats reporting a result about a configuration nobody chose.
func TestMain(m *testing.M) {
	testenv.MustCheckAmbient()
	os.Exit(m.Run())
}
