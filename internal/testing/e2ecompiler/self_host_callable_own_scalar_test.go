package e2ecompiler

import "testing"

func TestSelfHostCallableScalarOwn(t *testing.T) {
	const program = `enum Callback { Call((own boolean) => boolean) }
struct Holder { call: (own boolean) => boolean }
@noinline function flip(own b: boolean): boolean { return !b; }
function id[T](x: T): T { return x; }
function invoke[T](f: (own T) => T, own value: T): T { return f(value); }
@noinline function exercise(b: boolean): i32 {
    let f: (own boolean) => boolean = flip;
    let fs: ((own boolean) => boolean)[] = [f];
    let pair: ((own boolean) => boolean, i32) = (f, 7);
    let h = Holder { call: f };
    let boxed: Callback = Call(f);
    match (boxed) { Call(cb) => { if (cb(b) != !b) { return 3; } } }
    let same: (i32) => i32 = id;
    if (f(b) != !b || fs[0](b) != !b || pair.0(b) != !b || h.call(b) != !b) { return 1; }
    if (invoke(f, b) != !b || same(pair.1) != 7) { return 2; }
    return 0;
}
function main(): i32 {
    if (exercise(true) != 0 || exercise(false) != 0) { return 1; }
    print("ok");
    return 0;
}
`
	runSemanticProgram(t, "callable-scalar-own", program, []string{"exercise", "flip"}, semInlineWants("ok\n"))
}
