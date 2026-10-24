package e2ecompiler

import "testing"

var structMethodReorderCases = []struct {
	name    string
	main    string
	wantOut string
}{
	{"swap-receiver-params",
		`struct Pair[A, B] { first: A, second: B }
function (p: Pair[A, B]) swap[A, B](): Pair[B, A] {
    return Pair { first: p.second, second: p.first };
}
function main(): i32 {
    let p: Pair[i32, string] = Pair { first: 1, second: "s" };
    let q: Pair[string, i32] = p.swap();
    print(q.first);
    if (q.second != 1) { return 1; }
    return 0;
}`, "s\n"},
	{"swap-through-local",
		`struct Pair[A, B] { first: A, second: B }
function (p: Pair[A, B]) swap(): Pair[B, A] {
    let r = Pair { first: p.second, second: p.first };
    return r;
}
function main(): i32 {
    let p: Pair[i32, string] = Pair { first: 7, second: "t" };
    print(p.swap().first);
    return 0;
}`, "t\n"},
	{"receiver-spells-own-names",
		`struct Pair[A, B] { first: A, second: B }
function (p: Pair[X, Y]) swap[X, Y](): Pair[Y, X] {
    return Pair { first: p.second, second: p.first };
}
function (p: Pair[X, Y]) second_of(): Y { return p.second; }
function main(): i32 {
    let p: Pair[i32, string] = Pair { first: 1, second: "u" };
    print(p.second_of());
    print(p.swap().first);
    return 0;
}`, "u\nu\n"},
	{"spread-assigned-to-receiver",
		`struct Box[T] { n: i32, items: T[] }
function (b: Box[T]) renum(k: i32): Box[T] {
    b = Box { ...b, n: k };
    return b;
}
function main(): i32 {
    let b: Box[string] = Box { n: 1, items: ["x"] };
    b = b.renum(5);
    print(b.items[0]);
    if (b.n != 5) { return 1; }
    return 0;
}`, "x\n"},
	{"same-instantiation-in-tuple",
		`struct Box[T] { v: T, n: i32 }
function (b: Box[T]) bump(): (Box[T], i32) {
    return (Box { v: b.v, n: b.n + 1 }, b.n);
}
function main(): i32 {
    let b: Box[string] = Box { v: "w", n: 1 };
    let (c, k) = b.bump();
    print(c.v);
    if (c.n != 2 || k != 1) { return 1; }
    return 0;
}`, "w\n"},
}

func TestSelfHostStructMethodReorderIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range structMethodReorderCases {
		t.Run(tc.name, func(t *testing.T) {
			if out, code := runInterp(t, tc.main+"\n"); code != 0 || out != tc.wantOut {
				t.Fatalf("interpreter: out %q exit %d, want %q / 0", out, code, tc.wantOut)
			}
			if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.main)); code != 0 || out != tc.wantOut {
				t.Errorf("out %q exit %d, want %q / 0", out, code, tc.wantOut)
			}
		})
	}
}
