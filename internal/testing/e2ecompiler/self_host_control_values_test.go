package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostControlValuePatterns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, name := range []string{"basic", "string", "array", "nested", "literal_once", "enum_once", "diverge", "statement_arms", "flow", "lambda_tails"} {
		path := langSrcAbs(t, "conformance/cases/control_value_"+name+"/main.fern")
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(name+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d\n%s", code, stderr)
				}
				summary := leakSummaryLine(stderr)
				var allocs, frees, live int64
				if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
					t.Fatalf("census %q: %v", summary, err)
				}
				if allocs != frees || live != 0 {
					t.Fatalf("unbalanced census: %s", summary)
				}
				if strings.Contains(stderr, "fern-sanitizer:") {
					t.Fatal(stderr)
				}
			})
		}
	}
}

func TestSelfHostControlValueDiagnostics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, source, code string }{
		{"lambda_mixed", `function main(): i32 { let f = (n: i32): i32 => { match (n) { 0 => { 1 }, _ => { print("x"); } } }; return 0; }`, "E061"},
		{"mixed", `enum E { A(string), B } function main(): i32 { let e = E.A("s"); let x = if let E.A(s) = e { s } else { 3 }; return 0; }`, "E031"},
		{"scope", `enum E { A(i32), B } function main(): i32 { let e = E.A(1); let x = if let E.A(n) = e { n } else { 0 }; return n; }`, "E001"},
		{"empty", `enum E { A(i32), B } function main(): i32 { let e = E.A(1); let x = if let E.A(n) = e { print("x"); } else { 0 }; return 0; }`, "E061"},
		{"missing_else", `enum E { A(i32), B } function main(): i32 { return if let E.A(n) = E.A(1) { n }; }`, "P001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0644); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(cli.bin, "-check", path, cli.stdlib).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "error["+tc.code+"]") {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}
