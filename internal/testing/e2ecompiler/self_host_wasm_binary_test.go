package e2ecompiler

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostWasmBinary(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host binary-wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	asmWatPath := buildWatbinAssembler(t, gcc, runner, driverBin, dir)

	run := func(path string) (int, string) {
		cmd := exec.Command(wasmtime, "run", "--dir", dir, path)
		out, _ := cmd.Output()
		return cmd.ProcessState.ExitCode(), string(out)
	}

	cases := []struct {
		name   string
		source string
		exit   int
	}{
		{"return-literal", "function main(): i32 { return 42; }", 42},
		{"arithmetic", "function main(): i32 { return 1 + 2 * 3; }", 7},
		{"locals", "function main(): i32 { let x: i32 = 5; return x + 37; }", 42},
		{"subtraction", "function main(): i32 { let a: i32 = 100; let b: i32 = 58; return a - b; }", 42},
		{"bitwise", "function main(): i32 { return (10 & 6) + (10 | 1); }", 13},
		{"while-sum", "function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 5) { s = s + i; i = i + 1; } return s; }", 10},
		{"if-then", "function main(): i32 { let x: i32 = 5; if (x > 3) { return 1; } return 0; }", 1},
		{"return-in-if-in-loop", "function main(): i32 { let i: i32 = 0; while (i < 10) { if (i == 3) { return i; } i = i + 1; } return 99; }", 3},
		{"break-continue", "function main(): i32 { let i: i32 = 0; let s: i32 = 0; while (i < 10) { i = i + 1; if (i == 3) { continue; } if (i > 6) { break; } s = s + i; } return s; }", 18},
		{"short-circuit-and", "function main(): i32 { let a: i32 = 5; if (a > 1 && a < 10) { return 7; } return 0; }", 7},
		{"nested-loops", "function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 3) { let j: i32 = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		{"div-rem", "function main(): i32 { let n: i32 = 17; return n / 5 + n % 5; }", 5},
		{"shift-right", "function main(): i32 { return 100 >> 2; }", 25},
		{"shift-left", "function main(): i32 { return 5 << 3; }", 40},
		{"struct-fields", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 30, y: 12 }; return p.x + p.y; }", 42},
		{"struct-mutate", "struct C { n: i32 } function main(): i32 { let c = C { n: 5 }; c = C { ...c, n: c.n + 37 }; return c.n; }", 42},
		{"struct-nested", "struct Inner { v: i32 } struct Outer { inner: Inner, k: i32 } function main(): i32 { let o = Outer { inner: Inner { v: 8 }, k: 34 }; return o.inner.v + o.k; }", 42},
		{"i64-div", "function main(): i32 { let a: i64 = 5000000000; let b: i64 = 7; return ((a / 1000000000) + b) as i32; }", 12},
		{"i64-sub", "function main(): i32 { let a: i64 = 100; let b: i64 = 58; let c: i64 = a - b; return c as i32; }", 42},
		{"i64-mul-cmp", "function main(): i32 { let a: i64 = 1000000; let b: i64 = 1000000; let p: i64 = a * b; if (p > 999999999999) { return 1; } return 0; }", 1},
		{"str-len", "function main(): i32 { let s: string = \"hello\"; return s.len(); }", 5},
		{"str-index", "function main(): i32 { let s: string = \"abcdef\"; return s[3] as i32; }", 100},
		{"str-concat-len", "function main(): i32 { let a: string = \"foo\"; let b: string = a + \"barbaz\"; return b.len(); }", 9},
		{"str-compare", "function main(): i32 { if (\"apple\" < \"banana\") { return 7; } return 0; }", 7},
		{"str-write", "function main(): i32 { write(\"hello world\"); return 0; }", 0},
		{"str-builder", "function main(): i32 { let s: string = \"\"; let i: i32 = 0; while (i < 3) { s = s + \"ab\"; i = i + 1; } write(s); return s.len(); }", 6},
		{"closure-capture", "function adder(n: i32): (i32) => i32 { return (x: i32): i32 => { return x + n; }; } function main(): i32 { let a = adder(10); return a(5); }", 15},
		{"closure-capture-array", "function main(): i32 { let xs = [10, 20, 30]; let get = (i: i32): i32 => { return xs[i]; }; return get(0) + get(2); }", 40},
		{"lambda-as-arg", "function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply((x: i32): i32 => { return x * 7; }, 6); }", 42},
		{"f64-mul", "function main(): i32 { let x: f64 = 3.5; return (x * 2.0) as i32; }", 7},
		{"f64-sub", "function main(): i32 { let a: f64 = 10.5; let b: f64 = 3.5; return (a - b) as i32; }", 7},
		{"f64-compare", "function main(): i32 { let a: f64 = 2.5; if (a > 2.0 && a < 3.0) { return 42; } return 0; }", 42},
		{"f64-sqrt", "function main(): i32 { let a: f64 = 9.0; return (__sqrt_f64(a)) as i32; }", 3},
		{"f64-int-convert", "function main(): i32 { let n: i32 = 7; let x: f64 = n as f64; return (x + 0.5) as i32; }", 7},
		{"memory-grow", "function main(): i32 { let xs: i32[] = []; let i: i32 = 0; while (i < 300000) { xs = xs.append(i); i = i + 1; } return xs[299999] - xs[299998]; }", 1},
		{"scale-recursion-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(12) - 100; }", 44},
		{"scale-struct-array-list", "struct Node { v: i32, next_idx: i32 } function main(): i32 { let ns: Node[] = []; ns = ns.append(Node { v: 10, next_idx: 1 }); ns = ns.append(Node { v: 20, next_idx: 2 }); ns = ns.append(Node { v: 12, next_idx: 0 - 1 }); let sum: i32 = 0; let i: i32 = 0; while (i >= 0) { sum = sum + ns[i].v; i = ns[i].next_idx; } return sum; }", 42},
		{"scale-string-split", "function main(): i32 { let s: string = \"the quick brown fox\"; let words = s.split(\" \"); let total: i32 = 0; for w in words { total = total + w.len(); } return total + words.len(); }", 20},
		{"string-from-bytes", "function main(): i32 { let s: string = string_from_bytes_unchecked([65, 66, 67, 68]); return s.len() * 10 + ((s[0] as i32) - 65); }", 40},
		{"string-from-bytes-write", "function main(): i32 { let s: string = string_from_bytes_unchecked([104, 105]); write(s); return s.len(); }", 2},
		{"memchr-v128", "function main(): i32 { let s: string = \"aaaaaaaaaaaaaaaaaaaa*aaa\"; return __memchr(s, 42, 0) + 22; }", 42},
		{"memchr-v128-miss", "function main(): i32 { let s: string = \"aaaaaaaaaaaaaaaaaaaaaaaa\"; return __memchr(s, 42, 0) + 43; }", 42},
		{"ascii-run-v128", "function main(): i32 { let s: string = \"aaaaaaaaaaaaaaaaaaaaaaaa\"; return __ascii_run(s, 0) + 18; }", 42},
		{"scale-f64-v128", "function main(): i32 { let xs: f64[] = [1.0, 2.0, 3.0, 4.0, 5.0]; let ys: f64[] = __scale_f64(xs, 2.0); return (ys[0] + ys[4]) as i32 + 30; }", 42},
		{"inner-f64-v128", "function main(): i32 { let ys = __inner_mul_add_f64([2.0, 3.0], [1.0, 2.0, 3.0, 4.0, 5.0, 6.0], 1, 2, 3, 1.0); return (ys[0] + ys[2]) as i32 + 2; }", 42},
		{"random-i32", "function main(): i32 { let r: i32 = random_i32(); return (r - r) + 42; }", 42},
		{"putchar", "function main(): i32 { putchar(72); putchar(105); putchar(10); return 0; }", 0},
		{"eprint", "function main(): i32 { eprint(\"log\"); write(\"x\"); return 0; }", 0},
		{"exit", "function main(): i32 { write(\"a\"); exit(0); write(\"b\"); return 0; }", 0},
		{"dyn-dispatch-blocktype", "trait Sh { function area(self: Self): i32; } impl Sh for i32 { function area(self: Self): i32 { return self + 1; } } function total(xs: dyn Sh[]): i32 { let t: i32 = 0; for x in xs { t = t + x.area(); } return t; } function main(): i32 { return total([3, 4, 14]); }", 24},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("WAT emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, "target.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write target wat: %v", err)
			}
			watExit, watOut := run(watPath)
			if watExit != tc.exit {
				t.Fatalf("WAT path exited %d, want %d", watExit, tc.exit)
			}

			out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
			if err != nil {
				var stderr []byte
				if ee, ok := err.(*exec.ExitError); ok {
					stderr = ee.Stderr
				}
				asmWatBytes, _ := os.ReadFile(asmWatPath)
				t.Fatalf("run assembler: %v\ntarget.wat: %d bytes sha256=%x\nassembler.wat: %d bytes sha256=%x\nassembler stderr:\n%s",
					err, len(wat), sha256.Sum256(wat), len(asmWatBytes), sha256.Sum256(asmWatBytes), stderr)
			}
			var bs []byte
			for _, tok := range strings.Fields(string(out)) {
				n, err := strconv.Atoi(tok)
				if err != nil {
					t.Fatalf("bad byte %q: %v", tok, err)
				}
				if n < 0 || n > 255 {
					t.Fatalf("byte out of range: %d", n)
				}
				bs = append(bs, byte(n))
			}
			if len(bs) < 8 {
				t.Fatalf("binary too short: %d bytes", len(bs))
			}
			wasmPath := filepath.Join(dir, tc.name+".wasm")
			if err := os.WriteFile(wasmPath, bs, 0o644); err != nil {
				t.Fatalf("write wasm: %v", err)
			}

			if want, ok := wantSIMDMnemonics[tc.name]; ok {
				wasmTools, err := exec.LookPath("wasm-tools")
				if err != nil {
					t.Skip("wasm-tools not on PATH; skipping the SIMD disassembly check")
				}
				text, err := exec.Command(wasmTools, "print", wasmPath).Output()
				if err != nil {
					t.Fatalf("wasm-tools print: %v", err)
				}
				for _, m := range want {
					if !strings.Contains(string(text), m) {
						t.Errorf("disassembly has no %s — watbin encoded some other instruction", m)
					}
				}
			}

			binExit, binOut := run(wasmPath)
			if binExit != tc.exit {
				t.Errorf("binary module exited %d, want %d (WAT path: %d, %d bytes)", binExit, tc.exit, watExit, len(bs))
			}
			if binOut != watOut {
				t.Errorf("binary stdout = %q, want %q (WAT path)", binOut, watOut)
			}
		})
	}
}

var wantSIMDMnemonics = map[string][]string{
	"memchr-v128":      {"i8x16.splat", "v128.load", "i8x16.eq", "i8x16.bitmask", "i32.ctz"},
	"memchr-v128-miss": {"i8x16.splat", "v128.load", "i8x16.eq", "i8x16.bitmask", "i32.ctz"},
	"ascii-run-v128":   {"v128.load", "i8x16.bitmask", "i32.ctz"},
	"scale-f64-v128":   {"f64x2.splat", "v128.load", "f64x2.mul", "v128.store"},
	"inner-f64-v128":   {"f64x2.splat", "v128.load", "f64x2.mul", "f64x2.add", "v128.store"},
}

func buildWatbinAssembler(t *testing.T, gcc string, runner []string, driverBin, dir string) string {
	t.Helper()
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../../compiler", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(withPrintInt(asmReadFileDriver))
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("assembler emitter produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "assembler.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write assembler wat: %v", err)
	}
	return asmWatPath
}

func assembleWithWatbin(t *testing.T, wasmtime, asmWatPath, dir, name string) string {
	t.Helper()
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).CombinedOutput()
	if err != nil {
		t.Fatalf("run assembler: %v\n%s", err, out)
	}
	var bs []byte
	for _, tok := range strings.Fields(string(out)) {
		n, err := strconv.Atoi(tok)
		if err != nil || n < 0 || n > 255 {
			t.Fatalf("bad byte %q in assembler output", tok)
		}
		bs = append(bs, byte(n))
	}
	wasmPath := filepath.Join(dir, name+".wasm")
	if err := os.WriteFile(wasmPath, bs, 0o644); err != nil {
		t.Fatal(err)
	}
	return wasmPath
}

func TestSelfHostWasmBinaryComponentCoreShapes(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	asmWatPath := buildWatbinAssembler(t, gcc, runner, driverBin, dir)

	const wat = `(module
  (import "wasi_snapshot_preview1" "fd_write" (func $w1 (param i32 i32 i32 i32) (result i32)))
  (import "wasi_snapshot_preview1" "fd_write" (func $w2 (param i32 i32 i32 i32) (result i32)))
  (memory (export "memory") 1)
  (func $twice (param i32) (result i32) (local i32)
    (local.set 1 (i32.add (local.get 0) (local.get 0)))
    (local.get 1))
  (func $after_unnamed (param i32) (result i32) (local i32 i32) (local $n i32)
    (local.set 2 (i32.const 100))
    (local.set $n (i32.add (local.get 0) (i32.const 1)))
    (i32.add (local.get $n) (i32.sub (local.get 2) (i32.const 100))))
  (export "after_unnamed" (func $after_unnamed))
  (func $start
    (drop (call $w2 (i32.const 1) (i32.const 8) (i32.const 1) (i32.const 20)))
    (drop (call $w1 (i32.const 1) (i32.const 8) (i32.const 1) (i32.const 20)))
    (drop (call $twice (i32.const 3))))
  (export "_start" (func $start))
  (data (i32.const 8) "\10\00\00\00\03\00\00\00")
  (data (i32.const 16) "ok\n"))
`
	if err := os.WriteFile(filepath.Join(dir, "target.wat"), []byte(wat), 0o644); err != nil {
		t.Fatal(err)
	}
	wasmPath := assembleWithWatbin(t, wasmtime, asmWatPath, dir, "shapes")
	if out, err := exec.Command(wasmtools, "validate", wasmPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	text, err := exec.Command(wasmtools, "print", wasmPath).Output()
	if err != nil {
		t.Fatalf("wasm-tools print: %v", err)
	}
	if n := strings.Count(string(text), `(import "wasi_snapshot_preview1" "fd_write"`); n != 1 {
		t.Errorf("the module declares fd_write %d times; the assembler should have merged the two into one", n)
	}
	out, err := exec.Command(wasmtime, "run", wasmPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run: %v", err)
	}
	if string(out) != "ok\nok\n" {
		t.Errorf("stdout = %q; want both writes through the merged import", out)
	}
	out, err = exec.Command(wasmtime, "run", "--invoke", "after_unnamed", wasmPath, "41").Output()
	if err != nil {
		t.Fatalf("wasmtime --invoke after_unnamed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "42" {
		t.Errorf("after_unnamed(41) = %s, want 42: $n must be local 3, after the param and the two unnamed locals", got)
	}
}

const asmReadFileDriver = `
function main(): i32 {
    match (read_file("target.wat")) {
        Ok(wat) => {
            let bytes: i32[] = wat_to_binary(wat);
            let i: i32 = 0;
            while (i < bytes.len()) { print_int(bytes[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { eprint("assembler: read_file(target.wat) failed"); return 1; }
    }
    return 2;
}
`
