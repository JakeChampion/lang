package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostPayloadStructPatterns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	path := langSrcAbs(t, "conformance/cases/payload_struct_patterns/main.fern")
	for name, cmd := range map[string]*exec.Cmd{
		"Go-check":         exec.Command(oracle, "-check", path),
		"self-host-check":  runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
		"Go-interp":        exec.Command(oracle, "-interp", path),
		"self-host-interp": runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib),
	} {
		t.Run(name, func(t *testing.T) {
			out, err := cmd.CombinedOutput()
			if err != nil || len(out) != 0 {
				t.Fatalf("payload struct patterns: %v\n%s", err, out)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("payload struct patterns: exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
	testPayloadStructRefusals(t, cli, oracle)
}

func testPayloadStructRefusals(t *testing.T, cli *selfHostCLI, oracle string) {
	dir := t.TempDir()
	shapes, err := os.ReadFile(langSrcAbs(t, "conformance/cases/payload_struct_patterns/shapes.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shapes.fern"), shapes, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, code, source string }{
		{"incomplete-bool", "E030", `struct F { bit: boolean }
enum H { S(F) }
function f(h: H): i32 { return match (h) { S(F { bit: true }) => 1 }; }
function main(): i32 { return 0; }`},
		{"wrong-struct", "E035", `struct P { x: i32 }
struct Q { x: i32 }
enum H { S(P) }
function f(h: H): i32 { return match (h) { S(Q { x }) => x }; }
function main(): i32 { return 0; }`},
		{"unknown-field", "E043", `struct P { x: i32 }
enum H { S(P) }
function f(h: H): i32 { return match (h) { S(P { missing, .. }) => 1 }; }
function main(): i32 { return 0; }`},
		{"wrong-generic-struct", "E035", `struct P[T] { x: T }
struct Q[T] { x: T }
enum H { S(P[boolean]) }
function f(h: H): i32 { return match (h) { S(Q { x: true }) => 1, _ => 0 }; }
function main(): i32 { return 0; }`},
		{"primitive-slot", "E035", `struct P { x: i32 }
enum H { S(i32) }
function f(h: H): i32 { return match (h) { S(P { x }) => x }; }
function main(): i32 { return 0; }`},
		{"source-unreachable", "E026", `struct P { x: i32 }
enum H { S(P) }
function f(h: H): i32 { match (h) { S(P { x }) => { match (P { x: x }) { P { x: n } => { return n; }, _ => { return 0; } } } } return 0; }
function main(): i32 { return 0; }`},
		{"opaque-field", "E021", `import "./shapes";
enum H { S(shapes.Secret) }
function f(h: H): i32 { return match (h) { S(shapes.Secret { n, .. }) => n }; }
function main(): i32 { return 0; }`},
	} {
		t.Run("refusal/"+tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(tc.source+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for name, cmd := range map[string]*exec.Cmd{
				"Go":        exec.Command(oracle, "-check", path),
				"self-host": runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
			} {
				t.Run(name, func(t *testing.T) {
					out, err := cmd.CombinedOutput()
					if err == nil || !strings.Contains(string(out), "error["+tc.code+"]") {
						t.Fatalf("want %s, got %v\n%s", tc.code, err, out)
					}
					if tc.code == "E021" && strings.Count(string(out), "error[") != 1 {
						t.Fatalf("want one opaque-field error, got\n%s", out)
					}
				})
			}
		})
	}
}
