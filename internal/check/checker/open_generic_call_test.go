package checker

import (
	"strings"
	"testing"
)

func TestOpenGenericCallTakesTheTypeOfItsFirstUse(t *testing.T) {
	const decls = "struct Pairs[K, V] { ks: K[], vs: V[] }\n" +
		"function pairs[K, V](): Pairs[K, V] { return Pairs { ks: [], vs: [] }; }\n" +
		"function (p: Pairs[K, V]) put(k: K, v: V): Pairs[K, V] { return Pairs { ks: p.ks.append(k), vs: p.vs.append(v) }; }\n" +
		"function (p: Pairs[K, V]) size(): i32 { return p.ks.len(); }\n" +
		"function count(p: Pairs[i32, string]): i32 { return p.size(); }\n" +
		"function empty[T](): T[] { return []; }\n" +
		"function total(xs: i64[]): i32 { return xs.len(); }\n" +
		"enum Bag[T] { Nothing, One(T) }\n" +
		"function bag[T](): Bag[T] { return Nothing; }\n" +
		"function (b: Bag[T]) with(x: T): Bag[T] { return One(x); }\n"
	good := []struct{ name, body string }{
		{"method", "function main(): i32 { let p = pairs(); p = p.put(1, 2); return p.size(); }"},
		{"argument", "function main(): i32 { let p = pairs(); return count(p); }"},
		{"return", "function fresh(): Pairs[i32, i32] { let p = pairs(); return p; }\nfunction main(): i32 { return fresh().size(); }"},
		{"array argument", "function main(): i32 { let xs = empty(); return total(xs); }"},
		{"enum method", "function main(): i32 { let b = bag(); b = b.with(\"x\"); return 0; }"},
		{"method in a range loop", "function main(): i32 { let p = pairs(); for i in 0..4 { p = p.put(i * 7, i); } return p.size(); }"},
	}
	for _, c := range good {
		if err := checkSource(t, decls+c.body+"\n"); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	bad := []struct{ name, body, want string }{
		{"no use", "function main(): i32 { let p = pairs(); return 0; }", "could not infer type parameter K for pairs"},
		{"a second use at another type", "function main(): i32 { let p = pairs(); p = p.put(1, 2); p = p.put(\"a\", 3); return 0; }", "expected i32, got string"},
	}
	for _, c := range bad {
		err := checkSource(t, decls+c.body+"\n")
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got:\n%v", c.name, c.want, err)
		}
	}
}
