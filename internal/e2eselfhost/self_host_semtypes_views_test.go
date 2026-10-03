package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// Exact semantic identity must distinguish array storage from a borrowed
// view, including when the distinction occurs inside another type.
func TestSelfHostSemanticArrayViewIdentity(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	const source = `import "./semtypes";
import "./typeinfo";
function main(): i32 {
    let byte: typeinfo.Type = typeinfo.TypeI32 { width: 8, unsigned: true, is_char: false };
    let word: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false };
    let owned: typeinfo.Type = typeinfo.TypeArray { elem: byte, view: false };
    let view: typeinfo.Type = typeinfo.TypeArray { elem: byte, view: true };
    let words: typeinfo.Type = typeinfo.TypeArray { elem: word, view: true };
    if (!semtypes.equal(owned, owned) || !semtypes.equal(view, view)) { return 1; }
    if (semtypes.equal(owned, view) || semtypes.equal(view, owned)) { return 2; }
    if (!semtypes.lends_array(owned, view)) { return 3; }
    if (semtypes.lends_array(view, owned) || semtypes.lends_array(owned, owned)) { return 4; }
    if (semtypes.lends_array(owned, words)) { return 5; }
    let arrays: typeinfo.Type = typeinfo.TypeArray { elem: owned, view: false };
    let views: typeinfo.Type = typeinfo.TypeArray { elem: view, view: false };
    if (semtypes.equal(arrays, views)) { return 6; }
    let a: typeinfo.Type = typeinfo.TypeTuple { elements: [owned, word] };
    let b: typeinfo.Type = typeinfo.TypeTuple { elements: [view, word] };
    if (semtypes.equal(a, b)) { return 7; }
    let ma: typeinfo.Type = typeinfo.TypeMap { key: word, value: owned };
    let mb: typeinfo.Type = typeinfo.TypeMap { key: word, value: view };
    if (semtypes.equal(ma, mb)) { return 8; }
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "array_view_identity.fern"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "array_view_identity.fern", "array_view_identity")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("array view identity: %v\n%s", err, out)
	}
}

func TestSelfHostArrayViewLending(t *testing.T) {
	const source = `struct Holder { bytes: [u8] }
function lend(xs: u8[]): [u8] { return xs[:]; }
function second(xs: [u8]): i32 { return xs[1] as i32; }
function count[T](xs: [T]): i32 { return xs.len(); }
function text_bytes(s: string): [u8] { return s.as_bytes(); }
function main(): i32 {
  let xs: u8[] = [7, 255, 0];
  let view: [u8] = lend(xs);
  let alias: [u8] = view;
  let record: Holder = Holder { bytes: view };
  let pair: ([u8], i32) = (xs, 3);
  if (second(xs) != 255 || second(alias) != 255 || second(record.bytes) != 255) { return 1; }
  if (second(pair.0) != 255 || pair.1 != 3) { return 2; }
  if (count(xs) != 3 || count(view) != 3) { return 6; }
  let literal: [u8] = [8, 9];
  if (literal.len() != 2 || literal[0] != 8) { return 3; }
  let bytes: [u8] = text_bytes("hello");
  if (bytes.len() != 5 || bytes[4] != b'o') { return 4; }
  xs = xs.with(1, 42 as u8);
  if (second(xs) != 42 || second(alias) != 255 || second(pair.0) != 255) { return 5; }
  return 0;
}
`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, source, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("array lending: exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
