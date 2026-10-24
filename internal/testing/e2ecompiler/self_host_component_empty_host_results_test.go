package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

// Empty canonical results carry a pointer but do not allocate a heap block.
func TestSelfHostComponentEmptyHostResults(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	cases := []struct{ name, source string }{
		{"env", `function main(): i32 {
  for i in 0..3 { match (env("FERN_MISSING_TEST_KEY")) { Some(_) => { return 1; }, None => {} } }
  return 0;
}`},
		{"environ", `function main(): i32 {
  for i in 0..3 { for value in environ() { if (value.len() == 0) { return 1; } } }
  return 0;
}`},
		{"args", `function main(): i32 {
  for i in 0..3 {
    let values: string[] = args();
    if (values.len() == 0) { return 1; }
    for value in values { if (value == "unexpected") { return 1; } }
  }
  return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := mustWrite(t, t.TempDir(), "main.fern", tc.source)
			binary := cli.wasmComponent(t, src, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			for _, leg := range []struct {
				name string
				env  []string
				args []string
			}{
				{"empty-env-and-arg", nil, []string{""}},
				{"empty-value", []string{"--env", "EMPTY="}, []string{"x"}},
				{"values-and-empty-arg", []string{"--env", "A=x", "--env", "EMPTY="}, []string{"", "ok"}},
			} {
				t.Run(leg.name, func(t *testing.T) {
					args := append([]string{"run"}, leg.env...)
					args = append(append(args, binary), leg.args...)
					cmd := exec.Command("wasmtime", args...)
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					if err := cmd.Run(); err != nil {
						t.Fatalf("run: %v\n%s", err, stderr.String())
					}
					allocs, frees, live := leakSummaryOf(t, tc.name+"/"+leg.name, stderr.String())
					if allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("allocs=%d frees=%d live_bytes=%d, want balanced", allocs, frees, live)
					}
				})
			}
		})
	}
}
