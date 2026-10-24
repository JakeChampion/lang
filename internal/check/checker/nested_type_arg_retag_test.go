package checker

import "testing"

func TestNestedTypeArgIsRetagged(t *testing.T) {
	src := `struct Q { v: i32 }
struct W[T] { t: T }
struct V[T] { u: T }
function pass[T](x: T): T { return x; }
function main(): i32 {
    let fns: ((i32) => i32)[] = [(n: i32) => n + 1, (n: i32) => n * 2];
    let order: i32[] = [1, 0];
    let a: W[Q] = pass[W[Q]](W { t: Q { v: 3 } });
    let b: V[W[Q]] = pass[V[W[Q]]](V { u: a });
    return a.t.v + b.u.t.v * 10 + fns[order[0]](10);
}`
	if err := checkSource(t, src); err != nil {
		t.Fatal(err)
	}
}
