package e2e

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

const printableStdoutSeeds = 1024

func printableSeeds(t *testing.T) uint64 {
	t.Helper()
	if testing.Short() {
		return printableStdoutSeeds / 8
	}
	return printableStdoutSeeds
}

var printableKnownDivergences = map[uint64]map[string]string{}

func TestDifferential_PrintableStdout(t *testing.T) {
	have, missing := availableLegs(numBackendLegs)
	if len(have) == 0 {
		t.Fatalf("no backend can run here (missing: %s) — every seed would compare "+
			"the interpreter against nothing and the sweep would still report PASS",
			describeLegs(missing))
	}
	t.Logf("backend legs available here: %s", describeLegs(have))
	tally := newLegTally(have, numMinRunRatio)
	t.Cleanup(func() { tally.check(t) })

	for _, seed := range diffOracleWindow(t, printableSeeds(t)) {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			tally.seedCompared()
			assertNumProgramAgreesSkipping(t, fernsmith.GenPrintableMain(seed), printableKnownDivergences[seed], tally)
		})
	}
}

func FuzzGenerate_StdoutAgrees(f *testing.F) {
	for seed := uint64(0); seed < 16; seed++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		f.Add(b[:])
	}
	e2eharness.WarmSelfHostCLI(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		assertNumProgramAgrees(t, fernsmith.GenPrintableMainBytes(data))
	})
}
