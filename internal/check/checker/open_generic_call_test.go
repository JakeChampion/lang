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

func TestDestinationBindsACallInsideItsValue(t *testing.T) {
	const decls = "struct El[M] { kids: El[M][], tap: Option[M], pad: f64 }\n" +
		"function text[M](s: string): El[M] { return El { kids: [], tap: None, pad: 0.0 }; }\n" +
		"function column[M](kids: El[M][]): El[M] { return El { kids: kids, tap: None, pad: 0.0 }; }\n" +
		"function button[M](s: string, m: M): El[M] { return El { kids: [], tap: Some(m), pad: 0.0 }; }\n" +
		"function (e: El[M]) padded(v: f64): El[M] { return El { ...e, pad: v }; }\n" +
		"enum Msg { Inc, Dec }\n"
	const end = "\nfunction main(): i32 { return 0; }\n"
	good := []struct{ name, body string }{
		{"receiver", "function view(): El[Msg] { let e: El[Msg] = text(\"a\").padded(2.0); return e; }"},
		{"receiver of a receiver", "function view(): El[Msg] { return text(\"a\").padded(1.0).padded(2.0); }"},
		{"receiver in a generic function", "function wide[M](s: string): El[M] { return column([text(s).padded(1.0)]).padded(6.0).padded(7.0); }\nfunction view(): El[Msg] { return wide(\"a\"); }"},
		{"receivers in an array argument", "function view(): El[Msg] { return column([text(\"a\").padded(1.0), button(\"+\", Inc).padded(2.0)]).padded(3.0); }"},
		{"struct update base", "function view(): El[Msg] { return El { ...text(\"a\"), pad: 1.0 }; }"},
		{"struct update base in a generic function", "function wide[M](s: string): El[M] { return El { ...text(s), pad: 1.0 }; }\nfunction view(): El[Msg] { return wide(\"a\"); }"},
		{"tuple element", "function view(): El[Msg] { let p: (El[Msg], i32) = (text(\"a\").padded(5.0), 1); return p.0; }"},
	}
	for _, c := range good {
		if err := checkSource(t, decls+c.body+end); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	bad := []struct{ name, body, want string }{
		{"receiver with no destination", "function view(): i32 { let e = text(\"a\").padded(1.0); return 0; }", "could not infer type parameter M for text"},
		{"array with no destination", "function view(): i32 { let e = [text(\"a\")]; return 0; }", "could not infer type parameter M for text"},
	}
	for _, c := range bad {
		err := checkSource(t, decls+c.body+end)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got:\n%v", c.name, c.want, err)
		}
	}
}
