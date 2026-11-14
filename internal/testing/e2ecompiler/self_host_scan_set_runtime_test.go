package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A set that is not a constant takes the block scan through __fern_scan_set on
// the native targets. The program compares each scan with a scalar one: empty
// input, a member in each lane, after the last full block and in no place,
// members 0x00, 0x80 and 0xff, starts past 0, and sets shorter than 256.
func TestSelfHostScanSetRuntimeTable(t *testing.T) {
	selfHostRunOnEveryTarget(t, e2eharness.ScanSetRuntimeSource)
}
