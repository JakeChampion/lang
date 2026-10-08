package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A syntactically unreachable loop exit still needs a well-typed physical
// return. In particular WASM validates that tail even when it cannot execute.
func TestSelfHostUnreachableReturn(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct{ name, typ, value string }{
		{"i64", "i64", "4294967338i64"},
		{"u64", "u64", "18446744073709551615u64"},
		{"f64", "f64", "1.25"},
		{"f32", "f32", "1.25 as f32"},
		{"i32", "i32", "42"},
		{"boolean", "boolean", "true"},
		{"string", "string", `"retained result"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf(`
function forever(value: %s): %s {
  while (true) { return value; }
}
function main(): i32 {
  let value: %s = %s;
  let result: %s = forever(value);
  if (result != value) { return 1; }
  return 0;
}
`, tc.typ, tc.typ, tc.typ, tc.value, tc.typ)
			path := filepath.Join(t.TempDir(), "loop.fern")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertUnreachableReturnCensus(t, stderr)
				})
			}
		})
	}
	for _, typ := range []string{"i64", "f64"} {
		t.Run("exit-"+typ, func(t *testing.T) {
			source := fmt.Sprintf(`
function stop(): %s { while (true) { exit(7); } }
function main(): i32 { let value: %s = stop(); return value as i32; }
`, typ, typ)
			path := filepath.Join(t.TempDir(), "exit.fern")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 7 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertUnreachableReturnCensus(t, stderr)
				})
			}
		})
	}
}

// Scalar returns and literal strings need not allocate. Require the report and
// balance even when both counters are zero, unlike heap-allocation probes.
func assertUnreachableReturnCensus(t *testing.T, stderr string) {
	t.Helper()
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil {
		t.Fatalf("missing or invalid census: %v\n%s", err, stderr)
	}
	if allocs != frees || live != 0 {
		t.Fatalf("unbalanced census: %s", stderr)
	}
}
