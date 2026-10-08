package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func perceusTreeOracle(keys []int32, values []bool) (string, string) {
	m := make(map[int32]bool)
	for i, k := range keys {
		m[k] = values[i]
	}
	var ordered []int32
	for k := range m {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	var ks, vs []string
	for _, k := range ordered {
		ks = append(ks, fmt.Sprint(k))
		vs = append(vs, fmt.Sprint(m[k]))
	}
	return "[" + strings.Join(ks, ",") + "]", "[" + strings.Join(vs, ",") + "]"
}

func TestSelfHostPerceusRBTree(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "bench", "perceus", "rbtree.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rbtree.fern"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	src.WriteString(`import "./rbtree";
function walk(t: rbtree.Tree, keys: i32[], values: boolean[], at: i32, parent_red: boolean): (i32, i32) {
  match (t) {
    rbtree.Leaf => { return (at, 1); },
    rbtree.Node(color, left, key, value, right) => {
      let red: boolean = false;
      match (color) { rbtree.Red => { red = true; }, rbtree.Black => {} }
      if (parent_red && red) { return (-1, -1); }
      let (pos, height) = walk(left, keys, values, at, red);
      if (pos < 0 || pos >= keys.len()) { return (-1, -1); }
      if (keys[pos] != key || values[pos] != value) { return (-1, -1); }
      let (end, right_height) = walk(right, keys, values, pos + 1, red);
      if (end < 0 || height != right_height) { return (-1, -1); }
      if (red) { return (end, height); }
      return (end, height + 1);
    }
  }
}
function check(t: rbtree.Tree, keys: i32[], values: boolean[]): boolean {
  match (t) {
    rbtree.Node(rbtree.Red(), _, _, _, _) => { return false; },
    _ => {}
  }
  let (end, height) = walk(t, keys, values, 0, false);
  if (end != keys.len() || height < 1) { return false; }
  let count: i32 = 0;
  for v in values { if (v) { count = count + 1; } }
  return rbtree.count_true(t) == count;
}
function run(keys: i32[], values: boolean[], old_keys: i32[], old_values: boolean[], final_keys: i32[], final_values: boolean[]): boolean {
  let t: rbtree.Tree = rbtree.Leaf;
  let saved: rbtree.Tree = t;
  let i: i32 = 0;
  while (i < keys.len()) {
    t = rbtree.insert(t, keys[i], values[i]);
    i = i + 1;
    if (i == keys.len() / 2) { saved = t; }
  }
  return check(saved, old_keys, old_values) && check(t, final_keys, final_values);
}
function main(): i32 {
`)
	cases := [][]int32{nil, {7}, {3, 1, 2}, {1, 3, 2}, {0, -1, 1, -2147483648, 2147483647, 0, -1, 2147483647}}
	var ascending, descending []int32
	for i := int32(0); i < 64; i++ {
		ascending = append(ascending, i)
		descending = append(descending, 63-i)
	}
	cases = append(cases, ascending, descending)
	for i, keys := range cases {
		values := make([]bool, len(keys))
		var ks, vs []string
		for j, key := range keys {
			values[j] = j%3 == 0
			ks = append(ks, fmt.Sprint(key))
			vs = append(vs, fmt.Sprint(values[j]))
		}
		oldKeys, oldValues := perceusTreeOracle(keys[:len(keys)/2], values[:len(keys)/2])
		finalKeys, finalValues := perceusTreeOracle(keys, values)
		fmt.Fprintf(&src, "if (!run([%s], [%s], %s, %s, %s, %s)) { return %d; }\n", strings.Join(ks, ","), strings.Join(vs, ","), oldKeys, oldValues, finalKeys, finalValues, i+1)
	}
	for _, n := range []int{0, 1, 2, 31, 257} {
		var keys []int32
		var values []bool
		for i := n - 1; i >= 0; i-- {
			keys = append(keys, int32(i))
			values = append(values, i%10 == 0)
		}
		ks, vs := perceusTreeOracle(keys, values)
		fmt.Fprintf(&src, "if (!check(rbtree.make_tree(%d), %s, %s)) { return 20; }\n", n, ks, vs)
	}
	src.WriteString("return 0;\n}\n")
	path := filepath.Join(dir, "tree_test.fern")
	if err := os.WriteFile(path, []byte(src.String()), 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("interpreter", func(t *testing.T) {
		if out, err := exec.Command(buildLangBinForInterp(t), "-interp", path).CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			cli := buildSelfHostCLI(t)
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
