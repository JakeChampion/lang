package ir

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testenv"
)

// The rc lowering tests assert on op sequences that internal/ast's env-derived
// flags change: FERN_RC_REUSE_DROP_GUIDED selects a different reuse analysis,
// FERN_RC_FREE_DEBUG adds poisoning. See internal/testenv.
func TestMain(m *testing.M) {
	testenv.MustCheckAmbient()
	os.Exit(m.Run())
}
