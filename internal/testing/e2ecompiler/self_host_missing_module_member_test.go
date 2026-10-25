package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Missing module members retain their source name and the loader diagnostic.
func TestSelfHostMissingModuleMember(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	dir := t.TempDir()
	lib := `pub function visible(): i32 { return 1; }
pub function zebra(): i32 { return 2; }
pub const SHOWN: i32 = 3;
pub struct Box { n: i32 }
pub function (b: Box) value(): i32 { return b.n; }
pub enum Choice { One, Wrap(i32) }
function hidden(): i32 { return 4; }
`
	for name, source := range map[string]string{
		"lib.fern": lib,
		"facade.fern": `pub use "./lib".{ visible, Box, Choice };
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, source string
		reject       bool
	}{
		{"missing-call", `import "./lib"; function main(): i32 { return lib.missing(); }`, true},
		{"misspelt-call", `import "./lib"; function main(): i32 { return lib.visibl(); }`, true},
		{"missing-value", `import "./lib"; function main(): i32 { let f: () => i32 = lib.absent; return f(); }`, true},
		{"import-alias", `import "./lib" as api; function main(): i32 { return api.missing(); }`, true},
		{"method-as-function", `import "./lib"; function main(): i32 { return lib.value(lib.Box { n: 1 }); }`, true},
		{"method-suggestion", `import "./lib"; function main(): i32 { return lib.valu(lib.Box { n: 1 }); }`, true},
		{"private-call", `import "./lib"; function main(): i32 { return lib.hidden(); }`, true},
		{"before-later-shadow", `import "./lib"; function main(): i32 { let n = lib.missing(); let lib = lib.Box { n: 1 }; return n + lib.value(); }`, true},
		{"after-inner-shadow", `import "./lib"; function main(): i32 { if (true) { let lib = lib.Box { n: 1 }; lib.value(); } return lib.missing(); }`, true},
		{"public-call", `import "./lib"; function main(): i32 { return lib.visible(); }`, false},
		{"public-value", `import "./lib"; function main(): i32 { let f: () => i32 = lib.visible; return f(); }`, false},
		{"re-export", `import "./facade"; function main(): i32 { let b = facade.Box { n: 1 }; let x: facade.Choice = facade.Choice.Wrap(1); return facade.visible() + b.value(); }`, false},
		{"parameter-shadow", `import "./lib"; function f(lib: lib.Box): i32 { return lib.value(); } function main(): i32 { return f(lib.Box { n: 1 }); }`, false},
		{"local-shadow", `import "./lib"; function main(): i32 { let lib = lib.Box { n: 1 }; return lib.value(); }`, false},
		{"before-shadow", `import "./lib"; function main(): i32 { let f: () => i32 = lib.visible; let lib = lib.Box { n: 1 }; return f() + lib.value(); }`, false},
		{"loop-shadow", `import "./lib"; function main(): i32 { let items: lib.Box[] = [lib.Box { n: 1 }]; for lib in items { lib.value(); } return lib.visible(); }`, false},
		{"pattern-shadow", `import "./lib"; function main(): i32 { match (Some(lib.Box { n: 1 })) { Some(lib) => { return lib.value(); }, None => { return 0; } } }`, false},
		{"lambda-shadow", `import "./lib"; function main(): i32 { let f: (lib.Box) => i32 = (lib: lib.Box): i32 => { return lib.value(); }; return f(lib.Box { n: 1 }); }`, false},
	}
	header := regexp.MustCompile(`(?m)^.*:\d+:\d+: error\[[A-Z]\d+\]:[^\n]*`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(tc.source+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			goOutput, goErr := exec.Command(oracle, "-check", path).CombinedOutput()
			selfOutput, selfErr := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib).CombinedOutput()
			if !tc.reject {
				if goErr != nil || selfErr != nil {
					t.Fatalf("valid module reference: Go %v\n%s\nself %v\n%s", goErr, goOutput, selfErr, selfOutput)
				}
				return
			}
			want := header.FindAllString(string(goOutput), -1)
			got := header.FindAllString(string(selfOutput), -1)
			if goErr == nil || selfErr == nil || len(want) == 0 || strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("module diagnostic differs: Go %v\n%s\nself %v\n%s", goErr, goOutput, selfErr, selfOutput)
			}
			if !strings.Contains(strings.Join(want, "\n"), "error[E087]") {
				t.Fatalf("want an E087 loader refusal, got:\n%s", goOutput)
			}
		})
	}
}
