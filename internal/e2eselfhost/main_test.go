package e2eselfhost

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testenv"
)

// The self-host drivers read FERN_STRICT_IR / FERN_IR_VERIFY / FERN_LEAKCHECK
// and friends at emit time, and a driver started with cmd.Env == nil inherits
// whatever the shell exported. See internal/testenv.
func TestMain(m *testing.M) {
	testenv.MustCheckAmbient()
	os.Exit(m.Run())
}
