package e2ecompiler

import (
	"os"
	"testing"
)

// The self-host interpreter compares map keys as the compiled map does, and
// prints what every compiled target prints for these conformance cases:
// map_hand_written_eq_hash_keys, whose struct and enum key types each ignore
// part of the value in a derived or hand-written cmp.Eq (#12650), and
// char_eq_ord_hash, whose char-keyed maps include a literal (#12809).
func TestSelfHostInterpMapKeysCompareThroughEq(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, name := range []string{"map_hand_written_eq_hash_keys", "char_eq_ord_hash"} {
		t.Run(name, func(t *testing.T) {
			dir := "../../../conformance/cases/" + name + "/"
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
		})
	}
}
