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

// Copy dictionaries at each checkpoint, independent of the persistent tree.
func perceusCheckpointOracle(n, frequency int) [][]int64 {
	m := make(map[int64]bool)
	snapshot := func() []int64 {
		var keys []int64
		for key := range m {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		return keys
	}
	var reversed [][]int64
	for key := n; key > 0; key-- {
		m[int64(key)] = key%10 == 0
		if key%frequency == 0 {
			reversed = append(reversed, snapshot())
		}
	}
	result := [][]int64{snapshot()}
	for i := len(reversed) - 1; i >= 0; i-- {
		result = append(result, reversed[i])
	}
	return result
}

func perceusCheckpointLiterals(keys []int64) (string, string) {
	var ks, vs []string
	for _, k := range keys {
		ks = append(ks, fmt.Sprintf("%di64", k))
		vs = append(vs, fmt.Sprint(k%10 == 0 || k == 1099511627776))
	}
	return "[" + strings.Join(ks, ",") + "]", "[" + strings.Join(vs, ",") + "]"
}

func TestSelfHostPerceusCheckpoint(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "bench", "perceus", "rbtree_ck.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rbtree_ck.fern"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	src.WriteString(`import "./rbtree_ck";
function walk(t: rbtree_ck.Tree, keys: i64[], values: boolean[], at: i32, parent_red: boolean): (i32, i32) {
  match (t) {
    rbtree_ck.Leaf => { return (at, 1); },
    rbtree_ck.Node(color, left, key, value, right) => {
      let red: boolean = false;
      match (color) { rbtree_ck.Red => { red = true; }, rbtree_ck.Black => {} }
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
function check(t: rbtree_ck.Tree, keys: i64[], values: boolean[]): boolean {
  let (end, height) = walk(t, keys, values, 0, false);
  if (end != keys.len() || height < 1) { return false; }
  let count: i64 = 0i64;
  for v in values { if (v) { count = count + 1i64; } }
  return rbtree_ck.count_true(t) == count;
}
function check_all(trees: rbtree_ck.Trees, keys: i64[][], values: boolean[][]): boolean {
  let i: i32 = 0;
  let done: boolean = false;
  while (!done) {
    match (trees) {
      rbtree_ck.NoTrees => { done = true; },
      rbtree_ck.Cons(t, rest) => {
        if (i >= keys.len() || !check(t, keys[i], values[i])) { return false; }
        i = i + 1;
        trees = rest;
      }
    }
  }
  return i == keys.len();
}
function run(n: i64, frequency: i64, keys: i64[][], values: boolean[][], changed_keys: i64[], changed_values: boolean[]): boolean {
  let trees: rbtree_ck.Trees = rbtree_ck.make_tree(frequency, n);
  if (!check_all(trees, keys, values)) { return false; }
  match (trees) {
    rbtree_ck.Cons(head, _) => {
      let changed: rbtree_ck.Tree = rbtree_ck.insert(head, 1099511627776i64, true);
      return check(changed, changed_keys, changed_values) && check_all(trees, keys, values);
    },
    rbtree_ck.NoTrees => { return false; }
  }
}
function main(): i32 {
`)
	for i, tc := range [][2]int{{0, 5}, {1, 5}, {2, 1}, {5, 5}, {17, 1}, {64, 5}, {64, 7}, {129, 257}} {
		snapshots := perceusCheckpointOracle(tc[0], tc[1])
		var keys, values []string
		for _, row := range snapshots {
			ks, vs := perceusCheckpointLiterals(row)
			keys = append(keys, ks)
			values = append(values, vs)
		}
		changed := append(append([]int64(nil), snapshots[0]...), 1099511627776)
		cks, cvs := perceusCheckpointLiterals(changed)
		fmt.Fprintf(&src, "if (!run(%di64, %di64, [%s], [%s], %s, %s)) { return %d; }\n", tc[0], tc[1], strings.Join(keys, ","), strings.Join(values, ","), cks, cvs, i+1)
	}
	src.WriteString("return 0;\n}\n")
	path := filepath.Join(dir, "checkpoint_test.fern")
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
