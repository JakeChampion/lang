package e2eharness

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testenv"
)

// The harness reads its own budgets and driver-mode selectors from the
// environment (self_host_membudget.go, interp_driver.go), so an ambient value
// changes the numbers these tests assert on. See internal/testenv.
func TestMain(m *testing.M) {
	testenv.MustCheckAmbient()
	os.Exit(m.Run())
}
