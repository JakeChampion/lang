package effects

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// The solver is a monotone bitmask fixpoint over the call graph. These
// pin the properties the diagnostics depend on: recursion terminates,
// mutual recursion agrees, an indirect call is charged the union over
// escaping callables and no more, and every reached effect has a
// witness chain.

var table = map[string]string{
	"write_file":    "fs",
	"read_file":     "fs",
	"tcp_connect":   "net",
	"random_i32":    "random",
	"now_unix_ms":   "time",
	"env":           "env",
	"print":         "", // an untagged builtin: recorded, carries nothing
	"len":           "",
	"exit":          "",
	"int_to_string": "",
}

func solve(t *testing.T, src string) (*Graph, *Solution) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	declared := map[string]bool{}
	for _, fn := range prog.Funcs {
		declared[fn.Name] = true
	}
	g := Build(prog, func(n string) bool {
		if declared[n] {
			return false
		}
		_, ok := table[n]
		return ok
	})
	return g, Solve(g, table)
}

func names(t *testing.T, sol *Solution, fn string) string {
	t.Helper()
	return strings.Join(sol.Vocab.Names(sol.Rows[fn]), ",")
}

func TestSolveIsTransitive(t *testing.T) {
	_, sol := solve(t, `
function leaf(p: string): i32 { write_file(p, "x"); return 0; }
function mid(p: string): i32 { return leaf(p); }
function top(p: string): i32 { return mid(p); }
function main(): i32 { return top("a"); }`)
	for _, fn := range []string{"leaf", "mid", "top", "main"} {
		if got := names(t, sol, fn); got != "fs" {
			t.Errorf("%s: row = %q, want \"fs\"", fn, got)
		}
	}
}

// Recursion needs no special case: rows only grow and the lattice is a
// finite powerset, so the loop terminates on its own. A test that
// hangs here is the regression.
func TestSolveTerminatesOnRecursion(t *testing.T) {
	_, sol := solve(t, `
function ping(n: i32): i32 { if (n <= 0) { return 0; } return pong(n - 1); }
function pong(n: i32): i32 { write_file("a", "b"); return ping(n - 1); }
function main(): i32 { return ping(3); }`)
	if got := names(t, sol, "ping"); got != "fs" {
		t.Errorf("ping: row = %q, want \"fs\" through mutual recursion", got)
	}
}

// A call through a value is charged the union over everything a
// function value could be carrying — and nothing else. `quiet` never
// escapes, so its effect must not leak into the indirect row.
func TestIndirectCallChargesOnlyEscapingCallables(t *testing.T) {
	g, sol := solve(t, `
function noisy(n: i32): i32 { return n + random_i32(); }
function quiet(n: i32): i32 { write_file("a", "b"); return n; }
function apply(f: (i32) => i32, n: i32): i32 { return f(n); }
function main(): i32 { return apply(noisy, 1) + quiet(2); }`)
	if !g.Indirect["apply"] {
		t.Fatal("apply calls through a parameter; it should be marked Indirect")
	}
	if g.Escaping["quiet"] {
		t.Error("quiet is only ever called directly; it must not count as escaping")
	}
	if got := names(t, sol, "apply"); got != "random" {
		t.Errorf("apply: row = %q, want \"random\" (noisy escapes, quiet does not)", got)
	}
}

// A name in CALL position is not a function value. Getting this wrong
// makes every called function escape, which charges every indirect
// call with the whole program's effects.
func TestCallPositionIsNotAnEscape(t *testing.T) {
	g, _ := solve(t, `
function helper(n: i32): i32 { return n + 1; }
function main(): i32 { return helper(1); }`)
	if g.Escaping["helper"] {
		t.Error("helper is only called, never passed; it must not count as escaping")
	}
}

func TestWitnessNamesTheIndirectHop(t *testing.T) {
	g, sol := solve(t, `
function noisy(n: i32): i32 { return n + random_i32(); }
function apply(f: (i32) => i32, n: i32): i32 { return f(n); }
function main(): i32 { return apply(noisy, 1); }`)
	w := Witness(g, sol, "apply", sol.Rows["apply"])
	if len(w) != 1 {
		t.Fatalf("want one witness, got %d", len(w))
	}
	got := strings.Join(w[0].Path, " → ")
	want := "apply → " + IndirectNode + " → noisy → random_i32"
	if got != want {
		t.Errorf("witness = %q, want %q", got, want)
	}
}

func TestWitnessExplainsEveryReachedEffect(t *testing.T) {
	g, sol := solve(t, `
function busy(p: string): i32 {
    write_file(p, "x");
    return random_i32() + now_unix_ms();
}
function main(): i32 { return busy("a"); }`)
	w := Witness(g, sol, "busy", sol.Rows["busy"])
	if len(w) != len(sol.Vocab.Names(sol.Rows["busy"])) {
		t.Fatalf("every reached effect needs a witness: %d effects, %d chains", len(sol.Vocab.Names(sol.Rows["busy"])), len(w))
	}
	for _, c := range w {
		if len(c.Path) < 2 || c.Path[0] != "busy" {
			t.Errorf("chain for %s does not start at the function: %v", c.Label, c.Path)
		}
	}
}

// A lambda may be invoked anywhere, so its reach widens the indirect
// row — but it is also walked as part of its definer, so an
// immediately-applied one is not undercounted either.
func TestLambdaReachWidensTheIndirectRow(t *testing.T) {
	_, sol := solve(t, `
function apply(f: (i32) => i32, n: i32): i32 { return f(n); }
function build(): i32 {
    return apply((x: i32) => x + random_i32(), 1);
}
function main(): i32 { return build(); }`)
	if got := names(t, sol, "apply"); !strings.Contains(got, "random") {
		t.Errorf("apply: row = %q, want it to include \"random\" from the escaping lambda", got)
	}
	if got := names(t, sol, "build"); !strings.Contains(got, "random") {
		t.Errorf("build: row = %q, want it to include \"random\" from the lambda it defines", got)
	}
}

func TestVocabularyIsSortedAndDeterministic(t *testing.T) {
	v := NewVocabulary(table)
	want := []string{"env", "fs", "net", "random", "time"}
	if strings.Join(v.Labels, ",") != strings.Join(want, ",") {
		t.Fatalf("labels = %v, want %v (untagged builtins contribute none)", v.Labels, want)
	}
	all := Set(0)
	for _, l := range v.Labels {
		b, ok := v.Bit(l)
		if !ok {
			t.Fatalf("%q not in its own vocabulary", l)
		}
		all |= b
	}
	if strings.Join(v.Names(all), ",") != strings.Join(want, ",") {
		t.Errorf("Names(all) = %v, want %v", v.Names(all), want)
	}
}

var _ ast.Node = (*ast.Call)(nil)
