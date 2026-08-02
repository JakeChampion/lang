package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostStrOwnDump pins the slot-carried string-ownership facts of
// #4297 CS4 (docs/OWNERSHIP-TYPES-PLAN.md) — the self-host analogue of
// native internal/ast/ownership_test.go. The irlower_run driver's `-str-own`
// mode walks each function's top-level bindings through the REAL CS4
// machinery (str_binding_ownership seeding → mark_str_own → str_own_slot
// read-back → str_expr_ownership on a returned ident) and prints one
// `p/v/r <name>: <ownership>` line per string param / var binding / returned
// ident. The assertions cover the whole classification matrix:
//
//   - params are Borrowed (the owned-by-default `i >= n_params` rule);
//   - a literal is Static; a concat / str_to_upper / i32_to_string is Owned
//     (fresh box); a slice / .trim() is a View (the #4294 immortal rc=-1
//     box); a general call is Borrowed (the CS1 reclaim-oriented
//     conservative class — deliberately NOT native's leak-conservative
//     Owned; see str_producer_ownership's doc);
//   - an ident ALIAS of an Owned local demotes to Borrowed (an alias never
//     acquires ownership — freeing both would be the #4294 over-release),
//     while a View / Static source PROPAGATES verbatim (the alias references
//     the same immortal box);
//   - a REASSIGNMENT applies the flow-insensitive MEET (the `a` lines,
//     mirroring lower_stmt_assign): differing classes demote the slot to
//     Borrowed (`re`: Static then Owned — either box may be live at a later
//     drop), equal classes keep the fact (`keep`: Owned twice stays Owned,
//     the fresh-rebind shape);
//   - the read-side classifier (str_expr_ownership, the `r` line) reflects
//     the slot fact VERBATIM — an Owned local reads back owned.
//
// No codegen decision consults these facts yet (CS4 is behaviour-identical;
// the fixpoint + per-module run gates pin that); this test pins the facts
// themselves so the CS3 (#4355) / reuse-analysis consumers inherit a checked
// classifier instead of re-derived heuristics.
func TestSelfHostStrOwnDump(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	src := `function mk(): string { return "made"; }

function classify(s: string, t: string): string {
    var lit: string = "hello";
    var fresh: string = s + t;
    var up: string = str_to_upper(s);
    var view: string = s[1:3];
    var trimmed: string = s.trim();
    var alias: string = fresh;
    var viewalias: string = view;
    var litalias: string = lit;
    var fromcall: string = mk();
    var num: string = i32_to_string(7);
    var other: i32 = 5;
    var re: string = "lit2";
    re = s + t;
    var keep: string = s + t;
    keep = t + s;
    return fresh;
}

function main(): i32 {
    var r: string = classify("ab", "cd");
    return r.len();
}
`
	want := `== mk
== classify
p s: borrowed
p t: borrowed
v lit: static
v fresh: owned
v up: owned
v view: view
v trimmed: view
v alias: borrowed
v viewalias: view
v litalias: static
v fromcall: borrowed
v num: owned
v re: static
a re: borrowed
v keep: owned
a keep: owned
r fresh: owned
== main
v r: borrowed
`

	cmd := exec.Command(bin, "-str-own")
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("irlower_run -str-own: %v (output %q)", err, out)
	}
	if got := string(out); got != want {
		t.Fatalf("str-own dump mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}
