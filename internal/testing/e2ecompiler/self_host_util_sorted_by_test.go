package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// util.sorted_by is a stable sort: it agrees with sort.SliceStable on keys
// with many ties, and it orders the second field of equal keys as given.
func TestSelfHostUtilSortedByIsStable(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	var lits, want []string
	for _, n := range []int{0, 1, 2, 3, 7, 64, 1000} {
		type pair struct{ k, v int }
		ps := make([]pair, n)
		for i := range ps {
			ps[i] = pair{r.Intn(10), i}
		}
		var elems []string
		for _, p := range ps {
			elems = append(elems, fmt.Sprintf("P { k: %d, v: %d }", p.k, p.v))
		}
		lits = append(lits, "["+strings.Join(elems, ", ")+"]")
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].k < ps[j].k })
		var keys []string
		for _, p := range ps {
			keys = append(keys, fmt.Sprintf("%d:%d", p.k, p.v))
		}
		want = append(want, strings.Join(keys, " "))
	}
	var body strings.Builder
	for i, lit := range lits {
		fmt.Fprintf(&body, "  let xs%d: P[] = %s;\n  print(shown(util.sorted_by(xs%d, (a: P, b: P) => a.k > b.k)));\n", i, lit, i)
	}
	src := "import \"./util\";\n\nstruct P { k: i32, v: i32 }\n\n" +
		"function shown(ps: P[]): string {\n  let out = \"\";\n  for i in 0..ps.len() {\n    if (i > 0) {\n      out += \" \";\n    }\n    out += f\"{ps[i].k}:{ps[i].v}\";\n  }\n  return out;\n}\n\n" +
		"function main(): i32 {\n" + body.String() + "  return 0;\n}\n"
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "util.fern")
	if err := os.WriteFile(filepath.Join(dir, "sorted.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "sorted.fern", "sorted")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("sorted: %v\n%s", err, out)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("%d lines, want %d:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("case %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
}
