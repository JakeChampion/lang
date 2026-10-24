package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostImportedCastTypes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	reference := buildLangBinForInterp(t)
	cases := []struct{ name, library, main string }{
		{"array-cell", `pub struct Item { value: i32 }
pub function create(): Cell[Item[]] { return cell_new([] as Item[]); }
`, `let c = model.create();
assert(c.get().len() == 0);
c.set([model.Item { value: 42 }]);
assert(c.get()[0].value == 42);`},
		{"option-cell", `pub struct Item { value: i32 }
pub function create(): Cell[Option[Item]] { return cell_new(None as Option[Item]); }
`, `let c = model.create();
match (c.get()) { None => {}, Some(_) => { assert(false); } }
c.set(Some(model.Item { value: 42 }));
match (c.get()) { Some(item) => { assert(item.value == 42); }, None => { assert(false); } }`},
		{"checked-downcast", `pub trait Named { function value(self: Self): i32; }
pub struct Item { n: i32 }
pub struct Other { n: i32 }
impl Named for Item { function value(self: Self): i32 { return self.n; } }
impl Named for Other { function value(self: Self): i32 { return self.n; } }
@noinline
function take(v: dyn Named): i32 {
  match (v as? Item) { Some(item) => { return item.n; }, None => { return 0; } }
}
pub function exercise(): i32 { return take(Item { n: 42 }) + take(Other { n: 7 }); }
`, `assert(model.exercise() == 42);`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "model.fern"), []byte(tc.library), 0o644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "main.fern")
			main := "import \"./model\";\nfunction main(): i32 {\n" + tc.main + "\nreturn 0;\n}\n"
			if err := os.WriteFile(path, []byte(main), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"-check", "-interp"} {
				t.Run("reference"+mode, func(t *testing.T) {
					if out, err := exec.Command(reference, mode, path).CombinedOutput(); err != nil {
						t.Fatalf("reference %s: %v\n%s", mode, err, out)
					}
				})
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					if tc.name == "checked-downcast" {
						// Scalar downcasts can need no heap allocation.
						var allocs, frees, live int64
						if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != frees || live != 0 {
							t.Fatalf("unbalanced census: %v\n%s", err, stderr)
						}
					} else {
						assertBalancedCensus(t, stderr)
					}
				})
			}
		})
	}
}
