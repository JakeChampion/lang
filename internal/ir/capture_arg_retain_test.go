package ir

import (
	"strings"
	"testing"
)

// A closure forwarding its captured struct to an owned-by-default param
// retains the env's reference at the call, exactly as a local would: the
// hoisted body incs both the capture (ctx) and its borrowed param (t)
// before the callee's exit sweep releases them.
func TestCaptureRefArgToOwnedParamRetained(t *testing.T) {
	p := lowerSourceWith(t, `struct Ctx { decls: string[] }
struct Txn { headers: string[] }
struct Out { ctx: Ctx, txn: Txn, n: i32 }
function run_sub(ctx: Ctx, t: Txn, name: string): Out {
    var hs: string[] = t.headers.append(name);
    return Out { ctx: ctx, txn: Txn { headers: hs }, n: hs.len() };
}
function driver(decls: string[]): (string, Txn) => Out {
    var ctx: Ctx = Ctx { decls: decls };
    var runner: (string, Txn) => Out = (name: string, t: Txn): Out => { return run_sub(ctx, t, name); };
    return runner;
}`, 8)
	var lambda string
	for _, fn := range p.Funcs {
		if strings.HasPrefix(fn.Name, "__closure_lambda") {
			lambda = fn.Name
		}
	}
	if lambda == "" {
		t.Fatalf("no hoisted lambda in:\n%s", p)
	}
	if got := countRcIncs(p, lambda); got != 2 {
		t.Errorf("%s: %d rc incs before the run_sub call, want 2 (captured ctx + borrowed t):\n%s", lambda, got, p)
	}
}
