package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// A bare variant of an enum of the module that names it is not ambiguous,
// also when a module that it reaches through an import declares the same
// name. The reference is in a block arm of a match expression, where the
// checker looks for ambiguous variants.
func TestSelfHostOwnModuleVariantIsNotAmbiguous(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	files := map[string]string{
		"m0.fern": `pub enum R { Io(i32), Au(i32) }

pub function wrap(n: i32): R {
  return Io(n);
}
`,
		"m1.fern": `import "./m0";

pub function check(n: i32): Result[i32, i32] {
  return match (m0.wrap(n)) { m0.Io(v) => Ok(v), _ => Err(0) };
}
`,
		"m2.fern": `import "./m1";

pub enum S { Io(i32), Other }

pub function pick(n: i32): Result[i32, S] {
  let v = match (m1.check(n)) { Ok(value) => value, Err(e) => { return Err(Io(e)); } };
  return Ok(v);
}
`,
		"main.fern": `import "./m2";

function main(): i32 {
  return match (m2.pick(3)) { Ok(v) => v, Err(_) => 9 };
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if stderr, code := cli.exitOfFile(t, filepath.Join(dir, "main.fern"), "x86-64-linux", nil); code != 3 {
		t.Fatalf("exit %d, want 3\n%s", code, stderr)
	}
}
