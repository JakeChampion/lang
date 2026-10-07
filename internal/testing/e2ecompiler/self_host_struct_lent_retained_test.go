package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// A struct local lent to a callee that stores it into its result. The callee's
// borrowed parameter is inc'd where the struct literal takes it, so the caller's
// box ends the call shared, and the caller's exit sweep must walk its fields
// only on finding rc 1. Walking them unconditionally frees the enum and array
// the returned value still holds: a segfault once the loop reuses the memory.
// The shape is the parser's literal-match desugar,
// `let sugar = …; return with_match_sugar(chain, sugar);`.
const structLentRetainedSrc = `enum Expr { EIdent(string), ENum(i32) }
struct Arm { n: i32 }
struct Sugar { scrut: Expr, lits: Arm[] }
struct IfS { cond: i32, sugar: Sugar }
enum Stmt { SIf(IfS), SOther(i32) }

function with_sugar(st: Stmt, sugar: Sugar): Stmt {
    match (st) {
        SIf(i) => { return SIf(IfS { ...i, sugar: sugar }); },
        _ => { return st; }
    }
    return st;
}
function build(arms: Arm[]): Stmt {
    let chain: Stmt = SIf(IfS { cond: 0, sugar: Sugar { scrut: ENum(2), lits: [] } });
    let sugar: Sugar = Sugar { scrut: ENum(1), lits: arms };
    return with_sugar(chain, sugar);
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    let keep: Stmt[] = [];
    while (i < 4) {
        let arms: Arm[] = [Arm { n: i }, Arm { n: 3 }];
        keep = keep.append(build(arms));
        i = i + 1;
    }
    for s in keep {
        match (s) {
            SIf(f) => {
                for a in f.sugar.lits { total = total + a.n; }
                match (f.sugar.scrut) { ENum(n) => { total = total + n * 10; }, _ => {} }
            },
            _ => {}
        }
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return total;
}
`

func TestSelfHostStructLentRetained(t *testing.T) {
	const want = 58
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "struct_lent_retained.fern")
	if err := os.WriteFile(src, []byte(structLentRetainedSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("wasm32-wasi", func(t *testing.T) {
		stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi"))
		if exit != want {
			t.Fatalf("exit=%d, want %d\n%s", exit, want, stderr)
		}
	})
	t.Run("x86-64", func(t *testing.T) {
		bin := cli.x86Binary(t, src)
		stderr, exit := runWithStdin(t, cli.runner, bin, nil)
		if exit != want {
			t.Fatalf("exit=%d, want %d (stderr %q)", exit, want, stderr)
		}
	})
}
