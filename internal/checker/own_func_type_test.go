package checker

import (
	"strings"
	"testing"
)

// A function TYPE spells its consuming parameters (`(own T) => R`), so a
// consuming function and a lending one have DIFFERENT types and neither can
// be reached through the other's. Before that, `apply(eat, a)` for `eat(own
// xs: i32[])` type-checked against `(i32[]) => i32` and the two sides
// disagreed about who releases the argument at run time.

const ownFnConsumer = `function eat(own xs: i32[]): i32 { return xs.len(); }
function keep(xs: i32[]): i32 { return xs.len(); }
`

func wantCheckError(t *testing.T, name, src, want string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected an error, got none", name)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("%s: expected %q, got: %v", name, want, err)
	}
}

func TestOwnFuncTypeRejectsConsumingAtLendingSlot(t *testing.T) {
	wantCheckError(t, "consuming-at-lending", ownFnConsumer+`
function apply(f: (i32[]) => i32): i32 { return f([1, 2]); }
function main(): i32 { return apply(eat); }`,
		"expected (i32[]) => i32, got (own i32[]) => i32")
}

func TestOwnFuncTypeRejectsLendingAtConsumingSlot(t *testing.T) {
	wantCheckError(t, "lending-at-consuming", ownFnConsumer+`
function apply(f: (own i32[]) => i32): i32 { return f([1, 2]); }
function main(): i32 { return apply(keep); }`,
		"expected (own i32[]) => i32, got (i32[]) => i32")
}

func TestOwnFuncTypeAcceptsMatchingConsumer(t *testing.T) {
	wantOK(t, "consuming-at-consuming", ownFnConsumer+`
function apply(f: (own i32[]) => i32): i32 { return f([1, 2]); }
function main(): i32 { return apply(eat); }`)
}

// The call-site ownership guard reaches a call through a function VALUE: the
// consuming positions come from the callee's TYPE, which is the only
// declaration such a call has. Without it a borrowed argument was handed over
// and both sides released it.
func TestOwnFuncTypeGuardsIndirectCall(t *testing.T) {
	wantE051(t, "indirect-borrowed-arg", ownFnConsumer+`
function apply(f: (own i32[]) => i32, a: i32[]): i32 { return f(a); }
function main(): i32 { return apply(eat, [1, 2]); }`)
}

func TestOwnFuncTypeIndirectCallAcceptsOwnParam(t *testing.T) {
	wantOK(t, "indirect-own-arg", ownFnConsumer+`
function apply(f: (own i32[]) => i32, own a: i32[]): i32 { return f(a); }
function main(): i32 { return apply(eat, [1, 2]); }`)
}

// A scalar carries no reference, so a consuming position takes nothing from
// the caller and the guard has nothing to check. A generic `own T` reaches
// every instantiation, and refusing a scalar one would make the consuming
// convention unspellable for a fold whose accumulator is a count.
func TestOwnParamAdmitsScalarArgument(t *testing.T) {
	wantOK(t, "scalar-own-arg", `
function thread[T](own acc: T, n: i32): T {
    if (n <= 0) { return acc; }
    return thread(acc, n - 1);
}
function main(): i32 {
    var s: i32 = thread(7, 2);
    var c: i32 = 0;
    var t: i32 = thread(c, 1);
    return s - 7 + t;
}`)
}

// A lambda declares a consuming parameter the same way a declaration does.
func TestOwnLambdaParamMatchesOwnFuncType(t *testing.T) {
	wantOK(t, "own-lambda-param", `
function apply(f: (own i32[]) => i32): i32 { return f([1, 2]); }
function main(): i32 { return apply((own xs: i32[]) => xs.len()); }`)
}

// Handing a value to a consuming slot of a function VALUE is a move, exactly as
// it is for a declared own-func: reading the name afterwards is a use after
// move. The affine walk read the own-func registry alone, which has no entry
// for a callee reached through a value, so every such argument was classified a
// borrow and the read went unreported — a use-after-free the codegen then
// emitted, since the callee had released the buffer.
func TestOwnFuncTypeIndirectCallMovesArgument(t *testing.T) {
	wantCheckError(t, "indirect-own-use-after-move", ownFnConsumer+`
function apply(f: (own i32[]) => i32, own a: i32[]): i32 { var n = f(a); return n + a.len(); }
function main(): i32 { return apply(eat, [1, 2]); }`,
		`use of owned parameter "a" after it was consumed`)
}

// `own T` at a slot the call binds to a scalar is vacuous — nothing changes
// hands — so a lending scalar function stands in for it, as it does for the
// self-host, where own_flags drops the flag at a scalar.
func TestOwnFuncTypeScalarSlotAdmitsLendingCallee(t *testing.T) {
	wantOK(t, "own-slot-scalar-lending", `
function id(n: i32, a: i32): i32 { return a + n; }
function thread[T](own acc: T, n: i32, visit: (i32, own T) => T): T { return visit(n, acc); }
function main(): i32 { return thread(7, 2, id); }`)
}
