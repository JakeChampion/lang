package e2ecompiler

import (
	"os/exec"
	"testing"
)

var selfHostProgCases = []struct {
	name string
	src  string
	exit int
}{
	{"array-with", `function main(): i32 { let a: i32[] = [1, 2, 3]; a = a.with(1, 20); return a[0] + a[1] + a[2]; }`, 24},
	{"cell-get-set", `function main(): i32 { let c: Cell[i32] = cell_new(0); c.set(c.get() + 5); c.set(c.get() * 2); return c.get(); }`, 10},
	{"cell-shared", `function bump(c: Cell[i32]): void { c.set(c.get() + 1); } function main(): i32 { let c: Cell[i32] = cell_new(10); bump(c); bump(c); bump(c); return c.get(); }`, 13},
	{"cell-string", `function main(): i32 { let c: Cell[string] = cell_new("A"); c.set("Z"); return c.get()[0] as i32; }`, 90},
	{"cell-string-field", `struct Box { c: Cell[string] } function main(): i32 { let b: Box = Box { c: cell_new("ab") }; b.c.set("xyz"); return b.c.get().len(); }`, 3},
	{"cell-string-shared", `struct Box { c: Cell[string] } function bump(b: Box): void { b.c.set(b.c.get() + "!"); } function main(): i32 { let b: Box = Box { c: cell_new("hi") }; bump(b); bump(b); return b.c.get().len(); }`, 4},
	{"cell-i64", `function main(): i32 { let c: Cell[i64] = cell_new(5000000000i64); c.set(c.get() + 1000000000i64); return (c.get() / 1000000000i64) as i32; }`, 6},
	{"cell-f64", `function main(): i32 { let c: Cell[f64] = cell_new(3.5); c.set(c.get() + 2.5); return c.get() as i32; }`, 6},
	{"float-const-accessor", `const HALF: f64 = 2.5; function main(): i32 { return (HALF + 1.5) as i32; }`, 4},
	{"f32-cast-arith", `function main(): i32 { let x: i32 = 7; let y: f32 = x as f32; return (y * 2.0) as i32; }`, 14},
	{"float-struct-field", `struct P { x: f64 } function main(): i32 { let p: P = P { x: 3.5 }; return (p.x + 0.5) as i32; }`, 4},
	{"f32-struct-field", `struct Q { v: f32 } function main(): i32 { let q: Q = Q { v: 4.5 }; return (q.v + 0.5) as i32; }`, 5},
	{"float-tuple-elem", `function main(): i32 { let t: (f64, i32) = (3.5, 1); return (t.0 + 0.5) as i32; }`, 4},
	{"float-array-index", `function main(): i32 { let a: f64[] = [1.5, 2.5]; return (a[1] + 0.5) as i32; }`, 3},
	{"u64-struct-field", `struct S { v: u64 } function main(): i32 { let s: S = S { v: 18446744073709551615 as u64 }; return (s.v >> 60) as i32; }`, 15},
	{"u64-tuple-elem", `function main(): i32 { let t: (u64, i32) = (18446744073709551615 as u64, 0); return (t.0 >> 60) as i32; }`, 15},
	{"u64-method-ret", `struct S {} function (s: S) w(): u64 { return 18446744073709551615 as u64; } function main(): i32 { let s: S = S {}; return (s.w() >> 60) as i32; }`, 15},
	{"closure-opt-match", `function pick(x: i32): Option[i32] { if (x == 2) { return Some(x * 10); } return None; } function apply(f: (i32) => Option[i32]): i32 { match (f(2)) { Some(v) => { return v; }, None => { return 0; } } } function main(): i32 { return apply(pick); }`, 20},
	{"prim-recv-opt-match", `function (n: i64) tryadd(x: i64): Option[i64] { if (x > 0 as i64) { return Some(n + x); } return None; } function main(): i32 { let a: i64 = 3 as i64; match (a.tryadd(4 as i64)) { Some(v) => { return v as i32; }, None => { return 0; } } }`, 7},
	{"map-field-get-opt-match", `import "core/map";
struct R { caps: Map[string, i32] } function mk(): R { let m: Map[string, i32] = map_new(4); m = m.insert("a", 7); return R { caps: m }; } function main(): i32 { let r: R = mk(); match (r.caps.get("a")) { Some(v) => { return v; }, None => { return 0; } } }`, 7},
	{"array-build", `function main(): i32 { let xs: i32[] = [1,2,3]; let out: i32[] = Array.build((b: ArrayBuilder[i32]): void => { for x in xs { b.append(x * x); } }); return out[0] + out[1] + out[2]; }`, 14},
	{"array-with-chain", `function main(): i32 { let a: i32[] = [0, 0, 0]; a = a.with(0, 5); a = a.with(2, 7); return a[0] * 10 + a[2]; }`, 57},
	{"map-insert", `import "core/map";
function main(): i32 { let m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); m = m.insert(1,99); return m.get_or(1,-1) + m.get_or(2,-1); }`, 119},
	{"map-insert-fresh", `import "core/map";
function main(): i32 { let m: Map[i32,i32] = map_new(4); m = m.insert(5,7); return m.get_or(5,0); }`, 7},

	{"destructure-method-tuple", `struct Pair { hi: i32, lo: i32 }
function (p: Pair) swapped(): (Pair, i32) { return (Pair { hi: p.lo, lo: p.hi }, p.hi); }
function main(): i32 { let p: Pair = Pair { hi: 7, lo: 3 }; let (q, old) = p.swapped(); return q.hi + q.lo + old; }`, 17},

	{"map-remove", `import "core/map";
function main(): i32 { let m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); let ex: boolean = m.contains(1); let m2 = m.remove(1); if (!ex) { return 70; } return m2.get_or(2,-1); }`, 20},
	{"map-remove-reads-default", `import "core/map";
function main(): i32 { let m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); let ex: boolean = m.contains(2); let m2 = m.remove(2); if (!ex) { return 70; } return m2.get_or(1,-1) + (m2.get_or(2,-1) + 1); }`, 10},
	{"map-remove-absent", `import "core/map";
function main(): i32 { let m: Map[i32,i32] = map_new(8); m = m.insert(1,10); let ex: boolean = m.contains(9); let m2 = m.remove(9); if (ex) { return 1; } return m2.get_or(1,-1); }`, 10},
	{"smap-remove", `import "core/map";
function main(): i32 { let m: Map[string,i32] = map_new(8); m = m.insert("a",10); m = m.insert("b",20); let ex: boolean = m.contains("a"); let m2 = m.remove("a"); if (!ex) { return 70; } return m2.get_or("b",-1) + (m2.get_or("a",-1) + 1); }`, 20},
}

func TestSelfHostProgsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range selfHostProgCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostProgsArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range selfHostProgCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
