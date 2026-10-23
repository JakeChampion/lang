package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostQualifiedStructPatterns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	path := langSrcAbs(t, "conformance/cases/qualified_struct_patterns/main.fern")
	for name, cmd := range map[string]*exec.Cmd{
		"Go-check":         exec.Command(oracle, "-check", path),
		"self-host-check":  runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
		"Go-interp":        exec.Command(oracle, "-interp", path),
		"self-host-interp": runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib),
	} {
		t.Run(name, func(t *testing.T) {
			out, err := cmd.CombinedOutput()
			if err != nil || len(out) != 0 {
				t.Fatalf("qualified struct patterns: %v\n%s", err, out)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("qualified struct patterns: exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
	testQualifiedOpaquePatterns(t, cli, oracle)
}

func testQualifiedOpaquePatterns(t *testing.T, cli *selfHostCLI, oracle string) {
	dir := t.TempDir()
	shapes, err := os.ReadFile(langSrcAbs(t, "conformance/cases/qualified_struct_patterns/shapes.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shapes.fern"), shapes, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, source string }{
		{"let", `function main(): i32 { let shapes.Secret { n } = shapes.secret(); return n; }`},
		{"parameter", `function f(shapes.Secret { n }: shapes.Secret): i32 { return n; }
function main(): i32 { return f(shapes.secret()); }`},
		{"for", `function main(): i32 { for shapes.Secret { n } in [shapes.secret()] { return n; } return 0; }`},
		{"match", `function main(): i32 { match (shapes.secret()) { shapes.Secret { n } => { return n; } } }`},
		{"match-expression", `function main(): i32 { return match (shapes.secret()) { shapes.Secret { n } => n }; }`},
		{"if-let", `function main(): i32 { if let shapes.Secret { n } = shapes.secret() { return n; } return 0; }`},
		{"discard-field", `function main(): i32 { match (shapes.secret()) { shapes.Secret { n: _ } => { return 0; } } }`},
	} {
		t.Run("opaque/"+tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte("import \"./shapes\";\n"+tc.source+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for name, cmd := range map[string]*exec.Cmd{
				"Go":        exec.Command(oracle, "-check", path),
				"self-host": runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
			} {
				t.Run(name, func(t *testing.T) {
					out, err := cmd.CombinedOutput()
					text := string(out)
					access := "destructure"
					if tc.name == "let" || tc.name == "parameter" || tc.name == "for" {
						access = "destructure a field of"
					}
					if err == nil || strings.Count(text, "error[") != 1 || !strings.Contains(text, "error[E021]: cannot "+access+" opaque type shapes.Secret outside the module that defines it") {
						t.Fatalf("want one opaque-pattern E021, got %v\n%s", err, out)
					}
				})
			}
		})
	}
}
