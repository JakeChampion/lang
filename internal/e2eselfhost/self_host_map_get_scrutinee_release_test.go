package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A match over a builtin `m.get(k)` shallow-frees the Option box its answer
// came in, and the AST lowering did that only at the join after the arms, so
// an arm that `return`s left the box behind: one 40-byte block per early exit.
// It is now released inside the arm once the bindings are read, as the
// builtin-call scrutinees already were (#9038's neighbour). FERN_SEM_IR=
// selects the AST lowering; the semantic one already balanced.
const mapGetScrutineeSrc = `import "core/map";
@noinline
function find(m: Map[string, i32], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v + 40; }, None => { return 9; } }
}
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 20) { n = n + find(m, "b") + find(m, "z"); i = i + 1; }
    return n % 101;
}
`

func TestSelfHostMapGetScrutineeReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "map_get.fern")
	if err := os.WriteFile(src, []byte(mapGetScrutineeSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = (20 * (42 + 9)) % 101
	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		t.Run(strings.TrimSuffix(mode, "=1"), func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode, "FERN_SEM_IR="), nil)
			if exit != want || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
