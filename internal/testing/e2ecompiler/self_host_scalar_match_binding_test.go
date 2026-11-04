package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostScalarMatchBindings(t *testing.T) {
	path := filepath.Join(langSrcAbs(t, "conformance"), "cases", "scalar_match_bindings", "main.fern")
	host := selfHostCLIForHost(t)
	oracle := host.native
	for _, mode := range []string{"-check", "-interp"} {
		if out, err := exec.Command(oracle, mode, path).CombinedOutput(); err != nil {
			t.Fatalf("Go %s: %v\n%s", mode, err, out)
		}
		if out, err := exec.Command(host.cli, mode, path, host.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("self-host %s: %v\n%s", mode, err, out)
		}
	}
	targets := append([]ssaBackendTarget(nil), host.targets...)
	if wasmtime, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi", runner: []string{wasmtime, "run"}})
	}
	for _, target := range targets {
		t.Run(target.target, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "scalar-match")
			extra := []string{"-sanitize"}
			if target.target == "wasm32-wasi" {
				extra = append(extra, "-emit", "asm")
			}
			host.compileWith(t, target, path, binary, extra...)
			command := exec.Command(binary)
			if len(target.runner) > 0 {
				command = exec.Command(target.runner[0], append(target.runner[1:], binary)...)
			}
			command.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("scalar match: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
		})
	}
}

func TestSelfHostScalarMatchBindingDiagnostics(t *testing.T) {
	host := selfHostCLIForHost(t)
	oracle := host.native
	for _, form := range []string{"statement", "value"} {
		for _, tc := range []struct{ name, declarations, typ, arms, code string }{
			{"guard-type", "", "i32", "n when n => n, rest => rest", "E027"},
			{"guard-coverage", "", "i32", "n when n > 0 => n", "E030"},
			{"unreachable", "", "i32", "n => n, 0 => 0", "E026"},
			{"constructor", "enum Item { One, Two }", "i32", "One => 1, _ => 0", "E035"},
			{"qualified-constructor", "enum Item { One, Two }", "i32", "Item.One => 1, _ => 0", "E035"},
			{"payload-syntax", "", "i32", "n() => 1, _ => 0", "E035"},
			{"array", "", "i32[]", "items => items.len()", "E035"},
		} {
			t.Run(form+"/"+tc.name, func(t *testing.T) {
				body := "return match (tag) { " + tc.arms + " };"
				if form == "statement" {
					var arms []string
					for _, arm := range strings.Split(tc.arms, ", ") {
						head, value, ok := strings.Cut(arm, " => ")
						if !ok {
							t.Fatal("test arm has no result")
						}
						arms = append(arms, head+" => { return "+value+"; }")
					}
					body = "match (tag) { " + strings.Join(arms, ", ") + " }"
				}
				path := filepath.Join(t.TempDir(), "main.fern")
				source := fmt.Sprintf("%s function f(tag: %s): i32 { %s }\n", tc.declarations, tc.typ, body)
				if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				for _, front := range []struct {
					name string
					cmd  *exec.Cmd
				}{
					{"Go", exec.Command(oracle, "-check", path)},
					{"self-host", exec.Command(host.cli, "-check", path, host.stdlib)},
				} {
					out, err := front.cmd.CombinedOutput()
					if err == nil || !strings.Contains(string(out), "error["+tc.code+"]") {
						t.Errorf("%s must reject with %s: %v\n%s", front.name, tc.code, err, out)
					}
					if tc.code == "E035" && tc.typ == "i32" && !strings.Contains(string(out), "literal patterns, a binding name, or") {
						t.Errorf("%s must list scalar bindings among the valid patterns:\n%s", front.name, out)
					}
				}
			})
		}
	}
}
