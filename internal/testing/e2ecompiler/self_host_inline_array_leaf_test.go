package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const inlineArrayLeafProgram = `function add_pair(own xs: u64[], x: u64): u64[] {
  return xs.append(x).append(x + 1u64);
}

function mark_byte(own xs: u8[], at: i32, x: u8): u8[] {
  return xs.with(at, x);
}
function add_byte(own xs: u8[], x: u8): u8[] { return xs.append(x); }
function add_float(own xs: f32[], x: f32): f32[] { return xs.append(x); }
function add_bool(own xs: boolean[], x: boolean): boolean[] { return xs.append(x); }
function add_char(own xs: char[], x: char): char[] { return xs.append(x); }
@noinline function kept_pair(own xs: u64[], x: u64): u64[] {
  return xs.append(x).append(x + 1u64);
}
function add_text(own xs: string[], x: string): string[] { return xs.append(x); }
@noinline function exercise(): i32 {
  let first: u64[] = [1u64, 2u64];
  let old = first;
  let next = add_pair(first, 3u64);
  let last = add_pair(next, 5u64);
  if (old.len() != 2 || old[0] != 1u64 || last.len() != 6 || last[5] != 6u64) { return 1; }
  let bytes: u8[] = [1u8, 2u8, 3u8];
  let previous = bytes;
  let marked = mark_byte(bytes, 1, 9u8);
  let saved_marked = marked;
  let final = mark_byte(marked, 0, 8u8);
  if (previous[1] != 2u8 || saved_marked[0] != 1u8 || saved_marked[1] != 9u8 || final[0] != 8u8) { return 2; }
  let words: u64[] = [];
  let i: i32 = 0;
  while (i < 100) { words = add_pair(words, (2 * i) as u64); i = i + 1; }
  if (words.len() != 200 || words[199] != 199u64) { return 3; }
  let kept = kept_pair(words, 200u64);
  if (kept.len() != 202 || kept[201] != 201u64) { return 4; }
  let text = add_text(["a"], "b");
  let saved_text = text;
  let more = add_text(text, "c");
  if (saved_text.len() != 2 || saved_text[1] != "b" || more.len() != 3 || more[2] != "c") { return 5; }
  let packed = add_byte([], 255u8);
  let packed2 = add_byte(packed, 0u8);
  if (packed2.len() != 2 || packed2[0] != 255u8 || packed2[1] != 0u8) { return 6; }
  let floats = add_float([], 16777217.0 as f32);
  let floats2 = add_float(floats, 1.5 as f32);
  if (floats2[0] != (16777216.0 as f32) || floats2[1] != (1.5 as f32)) { return 7; }
  let bools = add_bool([], true);
  let bools2 = add_bool(bools, false);
  if (!bools2[0] || bools2[1]) { return 8; }
  let chars = add_char([], 128578 as char);
  let chars2 = add_char(chars, 65 as char);
  if (chars2[0] != (128578 as char) || chars2[1] != (65 as char)) { return 9; }
  return 0;
}
function main(): i32 {
  let result = exercise();
  if (result != 0) { return result; }
  print("ok"); return 0;
}
`

func TestSelfHostInlineScalarArrayLeaf(t *testing.T) {
	runSemanticProgram(t, "array-leaves", inlineArrayLeafProgram,
		[]string{"add_pair", "mark_byte", "add_byte", "add_float", "add_bool", "add_char", "kept_pair", "add_text", "exercise"},
		semInlineWants("ok\n"), "kept_pair", "add_text")

	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "array_leaves.fern")
	if err := os.WriteFile(src, []byte(inlineArrayLeafProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		path := filepath.Join(dir, target+".s")
		out, err := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", path, src, h.stdlib).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", target, err, out)
		}
		asm, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := condBranchBody(t, string(asm), "exercise")
		call := "call __fn_"
		if target == "arm64-linux" {
			call = "bl __fn_"
		}
		for _, name := range []string{"add_pair", "mark_byte"} {
			if strings.Contains(body, call+name) {
				t.Errorf("%s still calls tiny scalar-array helper %s", target, name)
			}
		}
		for _, name := range []string{"kept_pair", "add_text"} {
			if !strings.Contains(body, call+name) {
				t.Errorf("%s unexpectedly inlined %s", target, name)
			}
		}
	}
}

func TestSelfHostInlineScalarArrayLeafBounds(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, index := range []string{"-1", "args().len() + 8"} {
		source := fmt.Sprintf(`function place(own xs: u8[], at: i32): u8[] { return xs.with(at, 7u8); }
function main(): i32 {
  let xs = place([1u8], 0);
  let ys = place(xs, %s);
  return ys[0] as i32;
}`, index)
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+index, func(t *testing.T) {
				out, code := cli.exitOf(t, source, target)
				if code != 134 {
					t.Fatalf("exit %d, want bounds abort 134:\n%s", code, out)
				}
			})
		}
	}
}
