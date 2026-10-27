package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostMatchEmptyArrayResult(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, tc := range []struct{ name, declarations, body string }{
		{"payload-first", `enum Curve { Samples(i32[]), Identity } function make(): Curve { return Samples([1,2]); }`, `let xs = match (make()) { Samples(values) => values, _ => [] }; let changed = xs.with(0,7); if (xs[0] != 1 || changed[0] != 7) { return 1; } return 0;`},
		{"empty-first", `enum Curve { Identity, Samples(i32[]) } function make(): Curve { return Samples([1,2]); }`, `let xs = match (make()) { Identity => [], Samples(values) => values }; let changed = xs.with(0,7); if (xs[0] != 1 || changed[0] != 7) { return 1; } return 0;`},
		{"nested", `enum Curve { Samples(i32[][]), Identity } function make(): Curve { return Samples([[1,2]]); }`, `let xs = match (make()) { Samples(values) => values, _ => [] }; let changed = xs.with(0,[7,8]); if (xs[0][0] != 1 || changed[0][0] != 7) { return 1; } return 0;`},
		{"nested-empty-tree", `enum Curve { Samples(i32[][]), Identity } function make(): Curve { return Samples([[1,2]]); }`, `let xs = match (make()) { Samples(values) => values, _ => [[]] }; let changed = xs.with(0,[7,8]); if (xs[0][0] != 1 || changed[0][0] != 7) { return 1; } return 0;`},
		{"if-expression", `function make(): i32[] { return [1,2]; }`, `let xs = if (seed.len()>0) { make() } else { [] }; let changed = xs.with(0,7); if (xs[0]!=1 || changed[0]!=7) { return 1; } return 0;`},
		{"imported-generic", `import "payload";`, `let xs = match (payload.make(seed)) { payload.Values(values) => values, _ => [] }; let changed=xs.with(0,seed+"-changed"); if (xs[0]!=seed || changed[0]!=seed+"-changed") { return 1; } return 0;`},
		{"tuple", `enum Curve { Samples(i32[]), Identity } function make(): Curve { return Samples([1,2]); }`, `let (xs,n) = match (make()) { Samples(values) => (values,3), _ => ([],3) }; let changed = xs.with(0,7); if (xs[0] != 1 || changed[0] != 7 || n != 3) { return 1; } return 0;`},
		{"generic-owned", `enum Bag[T] { Values(T[]), Empty } function make(value: string): Bag[string] { return Values([value]); }`, `let xs = match (make(seed)) { Values(values) => values, _ => [] }; let changed = xs.with(0,seed+"-changed"); if (xs[0] != seed || changed[0] != seed+"-changed") { return 1; } return 0;`},
		{"nominal-owned", `struct Item { value: string } enum Bag { Values(Item[]), Empty } function make(value: string): Bag { return Values([Item { value: value }]); }`, `let xs = match (make(seed)) { Values(values) => values, _ => [] }; let changed = xs.with(0,Item { value: seed+"-changed" }); if (xs[0].value != seed || changed[0].value != seed+"-changed") { return 1; } return 0;`},
		{"early-return", `enum Curve { Samples(i32[]), Identity } function make(): Curve { return Samples([1,2]); }`, `let xs = match (make()) { Samples(values) => values, _ => { return 3; } }; let changed = xs.with(0,7); if (xs[0] != 1 || changed[0] != 7) { return 1; } return 0;`},
		{"explicit-control", `enum Curve { Samples(i32[]), Identity } function make(): Curve { return Samples([1,2]); }`, `let xs: i32[] = match (make()) { Samples(values) => values, _ => [] }; let changed = xs.with(0,7); if (xs[0] != 1 || changed[0] != 7) { return 1; } return 0;`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			module := `pub enum Bag[T] { Values(T[]), Empty } pub function make(value: string): Bag[string] { return Values([value]); }`
			if err := os.WriteFile(filepath.Join(dir, "payload.fern"), []byte(module), 0o644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "main.fern")
			source := tc.declarations + "\nfunction main(): i32 { let seed=args()[0]+\"-owned-match-array-payload\"; if (seed.len()<16) { return 90; } " + tc.body + " }\n"
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"-check", "-interp"} {
				if out, err := exec.Command(oracle, mode, path).CombinedOutput(); err != nil {
					t.Fatalf("Go %s: %v\n%s", mode, err, out)
				}
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("match array exit %d\n%s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
					if strings.Contains(stderr, "allocs=0 ") {
						t.Fatalf("expected a nonzero owned payload census\n%s", stderr)
					}
				})
			}
		})
	}
}

func TestSelfHostMatchEmptyArrayRejectsMixedArms(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	codes := regexp.MustCompile(`error\[E[0-9]+\]`)
	for _, tc := range []struct{ name, setup, expr string }{
		{"string-payload", "", `match (make()) { Samples(values) => values, _ => ["wrong"] }`},
		{"nested-depth", "", `match (make()) { Samples(values) => values, _ => [[1]] }`},
		{"owned-view", `let owned=[1]; let view: [i32]=owned[:];`, `match (make()) { Samples(values) => values, _ => view }`},
		{"literal-depth", "", `match (make()) { Samples(values) => values, _ => [[]] }`},
		{"unknown-call", "", `match (make()) { Samples(values) => values, _ => missing() }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			source := `enum Curve { Samples(i32[]), Identity } function make(): Curve { return Samples([1]); } function main(): i32 { ` + tc.setup + `let xs=` + tc.expr + `; return xs.len(); }`
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(oracle, "-check", path).CombinedOutput()
			want := strings.Join(codes.FindAllString(string(out), -1), ",")
			if err == nil || want == "" {
				t.Fatalf("expected Go rejection: %v\n%s", err, out)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "out"), path).CombinedOutput()
					got := strings.Join(codes.FindAllString(string(out), -1), ",")
					if err == nil || got != want {
						t.Fatalf("diagnostic sequence %s, got %s: %v\n%s", want, got, err, out)
					}
				})
			}
		})
	}
}
