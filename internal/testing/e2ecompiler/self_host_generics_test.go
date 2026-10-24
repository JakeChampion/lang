package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var genericsCases = []struct {
	name string
	src  string
	exit int
}{
	{"generic-id", "function id[T](x: T): T { return x; } function main(): i32 { let a = id(42); let s = id(\"hi\"); if (s == \"hi\") { return a; } return 0; }", 42},
	{"unit-variant-nested-argument", "struct Tag { id: i32 } enum Box[T] { Full(T), Tagged(Tag), Empty } function wrap[T](f: () => T, mode: i32): Box[T] { let v: T = f(); if (mode == 1) { return Tagged(Tag { id: 7 }); } if (mode == 2) { return Empty; } return Full(v); } function pick(f: () => Option[i32], mode: i32): i32 { match (wrap(f, mode)) { Full(v) => { return 40; }, Tagged(t) => { return t.id; }, Empty => { return 8; } } return 9; } function main(): i32 { let f: () => Option[i32] = (): Option[i32] => Some(40); return pick(f, 0) + pick(f, 1) - pick(f, 2) + 3; }", 42},
	{"generic-box", "struct Box[T] { value: T } function unbox[T](b: Box[T]): T { return b.value; } function main(): i32 { let b: Box[i32] = Box { value: 42 }; return unbox(b); }", 42},
	{"two-type-params-fn", "function fst[A, B](a: A, b: B): A { return a; } function main(): i32 { return fst(42, 99); }", 42},
	{"two-type-params-struct", "struct Pair[A, B] { fst: A, snd: B } function main(): i32 { let p = Pair { fst: 40, snd: 2 }; return p.fst + p.snd; }", 42},
	{"mixed-instantiation", "function id[T](x: T): T { return x; } function main(): i32 { let n: i32 = id(40); let s: string = id(\"hi\"); return n + s.len(); }", 42},
	{"variant-payload-struct-result", "struct Box[T] { n: i32, f: (i32, T) => i32 } struct Boxes[T] { items: Box[T][] } function add_i(a: i32, b: i32): i32 { return a + b; } function (b: Boxes[T]) add(n: i32, f: (i32, T) => i32): Result[Boxes[T], string] { return Ok(Boxes { items: b.items.append(Box { n: n, f: f }) }); } function main(): i32 { let b: Boxes[i32] = Boxes { items: [] }; match (b.add(40, add_i)) { Ok(b2) => { let g: (i32, i32) => i32 = b2.items[0].f; return g(b2.items[0].n, 2); }, Err(e) => { return 1; } } return 1; }", 42},
	{"append-chain-record-array-method", "struct P { x: i32 } function (xs: T[]) count(): i32 { return xs.len(); } function main(): i32 { let a: P[] = [P { x: 1 }]; return a.append(P { x: 2 }).count() * 21; }", 42},
	{"append-chain-in-generic-body", "struct P { x: i32 } function (xs: T[]) count(): i32 { return xs.len(); } function grow[T](xs: T[], x: T): i32 { return xs.append(x).count(); } function main(): i32 { let a: P[] = [P { x: 1 }]; return grow(a, P { x: 2 }) * 21; }", 42},
	{"append-arg-to-generic-in-generic-body", "struct P { x: i32 } function count_of[U](ys: U[]): i32 { return ys.len(); } function grow[T](xs: T[], x: T): i32 { return count_of(xs.append(x)); } function main(): i32 { let a: P[] = [P { x: 1 }]; return grow(a, P { x: 2 }) * 21; }", 42},
	{"with-arg-to-generic-in-generic-body", "struct P { x: i32 } function count_of[U](ys: U[]): i32 { return ys.len(); } function swap[T](xs: T[], x: T): i32 { return count_of(xs.with(0, x)) + 41; } function main(): i32 { let a: P[] = [P { x: 1 }]; return swap(a, P { x: 2 }); }", 42},
	{"append-let-in-generic-body", "struct P { x: i32 } function (xs: T[]) count(): i32 { return xs.len(); } function grow[T](xs: T[], x: T): i32 { let ys = xs.append(x); return ys.count() * 21; } function main(): i32 { let a: P[] = [P { x: 1 }]; return grow(a, P { x: 2 }); }", 42},
	{"variant-payload-struct-option", "struct Box[T] { n: i32, f: (i32, T) => i32 } function add_i(a: i32, b: i32): i32 { return a + b; } function boxed[T](n: i32, f: (i32, T) => i32): Option[Box[T]] { if (n < 0) { return None; } return Some(Box { n: n, f: f }); } function main(): i32 { match (boxed(40, add_i)) { Some(b) => { let g: (i32, i32) => i32 = b.f; return g(b.n, 2); }, None => { return 1; } } return 1; }", 42},
}

func TestSelfHostGenericsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range genericsCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostGenericsArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range genericsCases {
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
