package e2ecompiler

import "testing"

var genericErasureIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"pair-string-elem", "function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { let s = pair[string, i32](\"xy\", 3); return s.0.len() * 10 + s.1; }", 23},
	{"pair-string-elem-implicit", "function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { let s = pair(\"xy\", 3); return s.0.len() * 10 + s.1; }", 23},
	{"pair-destructure", "function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { let (a, b) = pair(\"xy\", 3); return a.len() * 10 + b; }", 23},
	{"wrap-single-tparam", "function wrap[T](x: T): (T, i32) { return (x, 1); } function main(): i32 { let s = wrap(\"abc\"); return s.0.len() * 10 + s.1; }", 31},
	{"pair-scalar-regress", "function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { let s = pair(4, 2); return s.0 * 10 + s.1; }", 42},
	{"concrete-pair-regress", "function pair(k: string, v: i32): (string, i32) { return (k, v); } function main(): i32 { let s = pair(\"xy\", 3); return s.0.len() * 10 + s.1; }", 23},
	{"id-string", "function idg[T](x: T): T { return x; } function main(): i32 { return idg(\"xyz\").len() + 39; }", 42},
	{"id-string-binding", "function idg[T](x: T): T { return x; } function main(): i32 { let s = idg(\"ab\" + \"c\"); return s.len() * 10 + 12; }", 42},
	{"id-scalar-regress", "function idg[T](x: T): T { return x; } function main(): i32 { return idg(42); }", 42},
	{"dup-strarr", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { let a = dup(\"xyz\"); return a[0].len() + a[1].len() + 36; }", 42},
	{"opt-generic-payload", "function some1[T](x: T): Option[T] { return Some(x); } function main(): i32 { let o = some1(\"hello\"); match (o) { Some(s) => { return s.len() + 37; }, None => { return 0; } } }", 42},
	{"dup-scalar-regress", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { let a = dup(20); return a[0] + a[1] + 2; }", 42},
	{"opt-scalar-regress", "function some1[T](x: T): Option[T] { return Some(x); } function main(): i32 { let o = some1(40); match (o) { Some(n) => { return n + 2; }, None => { return 0; } } }", 42},
	{"dup-f64arr", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { let a = dup(2.5); if (a[0] + a[1] > 4.0) { return 42; } return 7; }", 42},
	{"dup-i64arr", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { let a = dup(5000000000 as i64); if (a[0] + a[1] == 10000000000 as i64) { return 42; } return 7; }", 42},
	{"opt-try-string", "function some1[T](x: T): Option[T] { return Some(x); } function unwrap_len(): Option[i32] { let s = some1(\"hello\")?; return Some(s.len()); } function main(): i32 { match (unwrap_len()) { Some(n) => { return n + 37; }, None => { return 0; } } }", 42},
	{"result-try-string", "function okg[T](x: T): Result[T, i32] { return Ok(x); } function helper(): Result[i32, i32] { let s = okg(\"hello\")?; return Ok(s.len()); } function main(): i32 { match (helper()) { Ok(n) => { return n + 37; }, Err(e) => { return 0; } } }", 42},
	{"dup-i32arr-regress", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { let a = dup(20); return a[0] + a[1] + 2; }", 42},
	{"opt-try-scalar-regress", "function some1[T](x: T): Option[T] { return Some(x); } function unwrap(): Option[i32] { let n = some1(40)?; return Some(n + 2); } function main(): i32 { match (unwrap()) { Some(v) => { return v; }, None => { return 0; } } }", 42},
	{"opt-tuple-payload", "function mk[T](x: T): Option[(T, i32)] { return Some((x, 1)); } function main(): i32 { match (mk(\"abc\")) { Some(t) => { return t.0.len() + t.1 + 38; }, None => { return 0; } } }", 42},
	{"result-tuple-payload", "function mk[T](x: T): Result[(T, i32), i32] { return Ok((x, 1)); } function helper(): Result[i32, i32] { let t = mk(\"abc\")?; return Ok(t.0.len() + t.1); } function main(): i32 { match (helper()) { Ok(n) => { return n + 38; }, Err(e) => { return 0; } } }", 42},
	{"opt-tuple-two-var", "function mk[K, V](k: K, v: V): Option[(K, V)] { return Some((k, v)); } function main(): i32 { match (mk(\"ab\", \"cde\")) { Some(t) => { return t.0.len() * 10 + t.1.len(); }, None => { return 0; } } }", 23},
	{"id-scalar-fstring", "import \"std/i32\"; function idg[T](x: T): T { return x; } function main(): i32 { let s = f\"a{idg(5)}b\"; return s.len() * 14; }", 42},
	{"id-scalar-method-annotated", "import \"std/i32\"; function idg[T](x: T): T { return x; } function main(): i32 { let v: i32 = idg(5); let s = \"x\" + v.to_string(); return s.len() * 21; }", 42},
	{"id-enum-regress", "enum E { A, B } function idg[T](x: T): T { return x; } function main(): i32 { let e = idg(E.A); return match (e) { A => 42, B => 9 }; }", 42},
	{"single-letter-enum-return", "enum E { A, B } function mkE(): E { return E.A; } function main(): i32 { let e = mkE(); return match (e) { A => 42, B => 9 }; }", 42},
}

func TestSelfHostGenericErasureIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range genericErasureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostGenericErasureIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range genericErasureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
