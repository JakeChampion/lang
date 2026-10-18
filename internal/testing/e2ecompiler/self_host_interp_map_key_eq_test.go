package e2ecompiler

import (
	"os"
	"testing"
)

// The self-host interpreter compares struct and enum map keys through the key
// type's cmp.Eq `eq`, derived or hand-written, as the compiled map does
// (#12650): conformance/cases/map_hand_written_eq_hash_keys, whose key types
// each ignore part of the value, prints what every compiled target prints.
func TestSelfHostInterpMapKeysCompareThroughEq(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const dir = "../../../conformance/cases/map_hand_written_eq_hash_keys/"
	want, err := os.ReadFile(dir + "expected.stdout")
	if err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", dir+"main.fern", cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != string(want) {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}
