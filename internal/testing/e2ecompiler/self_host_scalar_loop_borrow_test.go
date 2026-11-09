package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const scalarLoopCopy = `
function append_bytes(own output: u8[], input: [u8]): u8[] {
  for byte in input { output = output.append(byte); }
  return output;
}
function main(): i32 {
  let input: u8[] = [1, 2, 3];
  let result = append_bytes(append_bytes([], input[:2]), [4u8, 5u8]);
  if (result.len() != 4 || input.len() != 3 || result[0] != 1u8 || result[3] != 5u8) { return 1; }
  return 0;
}`

func TestSelfHostScalarLoopBorrowParity(t *testing.T) {
	cases := []struct{ name, source, want string }{
		{"scalar-view", scalarLoopCopy, ""},
		{"scalar-view-slice", strings.Replace(scalarLoopCopy, "byte in input", "byte in input[:2]", 1), ""},
		{"scalar-array", strings.ReplaceAll(strings.Replace(scalarLoopCopy, "input: [u8]", "input: u8[]", 1), "input[:2]", "input"), ""},
		{"handle-elements", `struct Held { reader: Reader }
function append_rows(own output: Held[], input: [Held]): Held[] {
  for row in input { output = output.append(row); }
  return output;
}
function main(): i32 {
  let input = [Held { reader: stdin() }];
  let result = append_rows(append_rows([], input[:]), []);
  return result.len() + input.len();
}`, "E051"},
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
			bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "scalar-loop-check", target)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					command := runScaleTarget(t, target, bin)
					command.Stdin = strings.NewReader(tc.source)
					for label, ds := range map[string][]driverDiag{"Go": goCheckerDiags(t, dir, tc.source), "self-host": driverDiags(runCheckerDriver(t, command, tc.name))} {
						if tc.want == "" && len(ds) == 0 {
							continue
						}
						found := false
						for _, d := range ds {
							found = found || tc.want != "" && d.code == tc.want
						}
						if !found {
							t.Fatalf("%s: want %q, got %v", label, tc.want, ds)
						}
					}
				})
			}
		})
	}
}

func TestSelfHostScalarLoopBorrowRuns(t *testing.T) {
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, scalarLoopCopy, []string{"FERN_SANITIZE=1"})
			if out, err := runScaleTarget(t, target, bin).CombinedOutput(); err != nil {
				t.Fatalf("scalar loop: %v\n%s", err, out)
			}
		})
	}
}
