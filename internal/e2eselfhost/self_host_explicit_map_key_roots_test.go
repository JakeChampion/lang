package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostExplicitMapKeyMethods(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = `import "core/cmp";
import "core/map";
struct Key { code: i32, data: u8[] }
impl cmp.Hash for Key {
  function hash(self: Key): i32 { return 0; }
}
impl cmp.Eq for Key {
  function eq(self: Key, other: Key): boolean { return self.code == other.code; }
}
function main(): i32 {
  var m: Map[Key, i32] = map_new(4);
  var i: i32 = 0;
  while (i < 32) {
    m = m.insert(Key { code: i, data: [255 as u8, i as u8] }, i + 1);
    i = i + 1;
  }
  i = 0;
  while (i < 32) {
    if (m.get_or(Key { code: i, data: [] }, -1) != i + 1) { return 1; }
    i = i + 1;
  }
  m = m.insert(Key { code: 7, data: [128 as u8] }, 91);
  if (m.len() != 32 || m.get_or(Key { code: 7, data: [] }, -1) != 91) { return 2; }
  return 0;
}`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, source, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit=%d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostUnusedMapKeyMethodsStayUnreachable(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = `import "core/cmp";
struct Unused { code: i32 }
impl cmp.Hash for Unused {
  function hash(self: Unused): i32 { subprocess("true", [], ""); return 0; }
}
function main(): i32 { return 0; }
`
	src := filepath.Join(t.TempDir(), "unused.fern")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.bin, "-check", "-target", "wasm32-wasi", src, cli.stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unreachable hash method contributed target effects: %v\n%s", err, out)
	}
}
