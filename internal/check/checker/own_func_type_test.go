package checker

import (
	"strings"
	"testing"
)

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

func TestOwnParamAdmitsScalarArgument(t *testing.T) {
	wantOK(t, "scalar-own-arg", `
function thread[T](own acc: T, n: i32): T {
    if (n <= 0) { return acc; }
    return thread(acc, n - 1);
}
function main(): i32 {
    let s: i32 = thread(7, 2);
    let c: i32 = 0;
    let t: i32 = thread(c, 1);
    return s - 7 + t;
}`)
}

func TestOwnLambdaParamMatchesOwnFuncType(t *testing.T) {
	wantOK(t, "own-lambda-param", `
function apply(f: (own i32[]) => i32): i32 { return f([1, 2]); }
function main(): i32 { return apply((own xs: i32[]) => xs.len()); }`)
}

func TestOwnFuncTypeIndirectCallMovesArgument(t *testing.T) {
	wantCheckError(t, "indirect-own-use-after-move", ownFnConsumer+`
function apply(f: (own i32[]) => i32, own a: i32[]): i32 { let n = f(a); return n + a.len(); }
function main(): i32 { return apply(eat, [1, 2]); }`,
		`use of owned parameter "a" after it was consumed`)
}

func TestOwnFuncTypeScalarSlotAdmitsLendingCallee(t *testing.T) {
	wantOK(t, "own-slot-scalar-lending", `
function id(n: i32, a: i32): i32 { return a + n; }
function thread[T](own acc: T, n: i32, visit: (i32, own T) => T): T { return visit(n, acc); }
function main(): i32 { return thread(7, 2, id); }`)
}
