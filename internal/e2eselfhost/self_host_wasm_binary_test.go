package e2eselfhost

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostWasmBinary exercises the self-hosted *binary* wasm emitter
// end to end — the module-walker + opcode encoder (wat_emit_bin.fern) on
// top of leb128 / wat_lex / wat_parse / wat_encode.
//
// The "assembler" is the five binary-encoder modules concatenated with a
// driver that read_file()s a target WAT, runs it through tokenize -> parse
// -> emit_binary, and prints the resulting bytes as newline-separated
// decimals. It is itself compiled through the self-host wasm pipeline and
// built ONCE; reading the WAT from a file (rather than embedding it in a
// string literal) keeps it independent of program size — large modules
// (maps, the string runtime) assemble fine.
//
// For each program the test:
//  1. runs the WAT emitter (wasm_run) to get the textual module + its
//     reference exit code / stdout under wasmtime,
//  2. writes that WAT into the preopened dir and runs the assembler to get
//     the module bytes,
//  3. reassembles the bytes into a .wasm and runs it,
//  4. asserts the binary module's exit code + stdout match the WAT path
//     (and the declared expected exit).
func TestSelfHostWasmBinary(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host binary-wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// Build the assembler once: the encoder modules + a read_file driver.
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(asmReadFileDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("assembler emitter produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "assembler.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write assembler wat: %v", err)
	}

	// run executes a WAT/wasm module under wasmtime (preopening dir for the
	// assembler's read_file), returning exit code + stdout.
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
		{"locals", "function main(): i32 { var x: i32 = 5; return x + 37; }", 42},
		{"subtraction", "function main(): i32 { var a: i32 = 100; var b: i32 = 58; return a - b; }", 42},
		{"bitwise", "function main(): i32 { return (10 & 6) + (10 | 1); }", 13},
		// Control flow.
		{"while-sum", "function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 5) { s = s + i; i = i + 1; } return s; }", 10},
		{"if-then", "function main(): i32 { var x: i32 = 5; if (x > 3) { return 1; } return 0; }", 1},
		{"return-in-if-in-loop", "function main(): i32 { var i: i32 = 0; while (i < 10) { if (i == 3) { return i; } i = i + 1; } return 99; }", 3},
		{"break-continue", "function main(): i32 { var i: i32 = 0; var s: i32 = 0; while (i < 10) { i = i + 1; if (i == 3) { continue; } if (i > 6) { break; } s = s + i; } return s; }", 18},
		{"short-circuit-and", "function main(): i32 { var a: i32 = 5; if (a > 1 && a < 10) { return 7; } return 0; }", 7},
		{"nested-loops", "function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 3) { var j: i32 = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		// Division / shifts / structs.
		{"div-rem", "function main(): i32 { var n: i32 = 17; return n / 5 + n % 5; }", 5},
		{"shift-right", "function main(): i32 { return 100 >> 2; }", 25},
		{"shift-left", "function main(): i32 { return 5 << 3; }", 40},
		{"struct-fields", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 30, y: 12 }; return p.x + p.y; }", 42},
		{"struct-mutate", "struct C { n: i32 } function main(): i32 { var c = C { n: 5 }; c.n = c.n + 37; return c.n; }", 42},
		{"struct-nested", "struct Inner { v: i32 } struct Outer { inner: Inner, k: i32 } function main(): i32 { var o = Outer { inner: Inner { v: 8 }, k: 34 }; return o.inner.v + o.k; }", 42},
		// i64.
		{"i64-div", "function main(): i32 { var a: i64 = 5000000000; var b: i64 = 7; return ((a / 1000000000) + b) as i32; }", 12},
		{"i64-sub", "function main(): i32 { var a: i64 = 100; var b: i64 = 58; var c: i64 = a - b; return c as i32; }", 42},
		{"i64-mul-cmp", "function main(): i32 { var a: i64 = 1000000; var b: i64 = 1000000; var p: i64 = a * b; if (p > 999999999999) { return 1; } return 0; }", 1},
		// Strings (including stdout via write()).
		{"str-len", "function main(): i32 { var s: string = \"hello\"; return s.len(); }", 5},
		{"str-index", "function main(): i32 { var s: string = \"abcdef\"; return s[3] as i32; }", 100},
		{"str-concat-len", "function main(): i32 { var a: string = \"foo\"; var b: string = a + \"barbaz\"; return b.len(); }", 9},
		{"str-compare", "function main(): i32 { if (\"apple\" < \"banana\") { return 7; } return 0; }", 7},
		{"str-write", "function main(): i32 { write(\"hello world\"); return 0; }", 0},
		{"str-builder", "function main(): i32 { var s: string = \"\"; var i: i32 = 0; while (i < 3) { s = s + \"ab\"; i = i + 1; } write(s); return s.len(); }", 6},
		// Maps — now testable via the read_file harness (the 33 KB map WAT
		// overran the old embed-the-WAT approach).
		{"map-get", "function main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; return m.get_or(2, 0) + m.get_or(3, 0); }", 50},
		{"map-string-key", "function main(): i32 { var m = map_new(8); m = m.insert(\"k\", 41); return m.get_or(\"k\", 0) + 1; }", 42},
		// Closures — named `(type $clos*)` decls + the table & elem sections
		// + call_indirect through the function table.
		{"closure-capture", "function adder(n: i32): fn { return (x: i32): i32 => { return x + n; }; } function main(): i32 { var a = adder(10); return a(5); }", 15},
		{"closure-capture-array", "function main(): i32 { var xs = [10, 20, 30]; var get = (i: i32): i32 => { return xs[i]; }; return get(0) + get(2); }", 40},
		{"lambda-as-arg", "function apply(f: fn, v: i32): i32 { return f(v); } function main(): i32 { return apply((x: i32): i32 => { return x * 7; }, 6); }", 42},
		// f64: f64.const (8-byte IEEE-754 immediate via f64_bits), the f64
		// arithmetic / comparison ops, the math intrinsics, and the
		// int<->float conversions.
		{"f64-mul", "function main(): i32 { var x: f64 = 3.5; return (x * 2.0) as i32; }", 7},
		{"f64-sub", "function main(): i32 { var a: f64 = 10.5; var b: f64 = 3.5; return (a - b) as i32; }", 7},
		{"f64-compare", "function main(): i32 { var a: f64 = 2.5; if (a > 2.0 && a < 3.0) { return 42; } return 0; }", 42},
		{"f64-sqrt", "function main(): i32 { var a: f64 = 9.0; return (__sqrt_f64(a)) as i32; }", 3},
		{"f64-int-convert", "function main(): i32 { var n: i32 = 7; var x: f64 = n as f64; return (x + 0.5) as i32; }", 7},
		// memory.grow: a program allocating past the initial 16 pages (1 MB)
		// now grows linear memory instead of trapping (and the encoder emits
		// memory.size / memory.grow).
		{"memory-grow", "function main(): i32 { var xs: i32[] = []; var i: i32 = 0; while (i < 300000) { xs = xs.append(i); i = i + 1; } return xs[299999] - xs[299998]; }", 1},
		// Integration capstone: string[] + a string-keyed count map + a
		// loop. Its ~34 KB WAT also exercises the assembler's own grown heap
		// (it OOM'd before memory.grow).
		{"integration-wordcount", "function main(): i32 { var words: string[] = [\"a\", \"b\", \"a\", \"c\", \"a\", \"b\"]; var counts = map_new(8); var i: i32 = 0; while (i < words.len()) { var w: string = words[i]; counts = counts.insert(w, counts.get_or(w, 0) + 1); i = i + 1; } return counts.get_or(\"a\", 0) * 10 + counts.get_or(\"b\", 0); }", 32},
		// At-scale validation: substantial multi-feature programs round-trip
		// through the binary encoder — deep recursion, a struct-array
		// "linked list" walked by index, and a string split + iteration.
		{"scale-recursion-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(12) - 100; }", 44},
		{"scale-struct-array-list", "struct Node { v: i32, next_idx: i32 } function main(): i32 { var ns: Node[] = []; ns = ns.append(Node { v: 10, next_idx: 1 }); ns = ns.append(Node { v: 20, next_idx: 2 }); ns = ns.append(Node { v: 12, next_idx: 0 - 1 }); var sum: i32 = 0; var i: i32 = 0; while (i >= 0) { sum = sum + ns[i].v; i = ns[i].next_idx; } return sum; }", 42},
		{"scale-string-split", "function main(): i32 { var s: string = \"the quick brown fox\"; var words = s.split(\" \"); var total: i32 = 0; for w in words { total = total + w.len(); } return total + words.len(); }", 20},
		// string_from_bytes_unchecked: pack a u8[] (i32[] of byte values) into a string
		// block. len() * 10 + first byte offset from 'A': 4*10 + (65-65) = 40.
		{"string-from-bytes", "function main(): i32 { var s: string = string_from_bytes_unchecked([65, 66, 67, 68]); return s.len() * 10 + ((s[0] as i32) - 65); }", 40},
		{"string-from-bytes-write", "function main(): i32 { var s: string = string_from_bytes_unchecked([104, 105]); write(s); return s.len(); }", 2},
		// The v128 kernels (ATLAS-PLATFORM-PLAN §3). This is the only path
		// that puts watbin's SIMD encodings under load: the WAT leg is parsed
		// by wasmtime, the binary leg by watbin, and the two must agree — which
		// is the check a pinned opcode table cannot make on its own, because
		// the 0xFD sub-opcode space is dense enough that a wrong byte is
		// usually another VALID instruction. Each haystack's answer is past the
		// first 16-byte block so the vector loop actually runs.
		{"memchr-v128", "function main(): i32 { var s: string = \"aaaaaaaaaaaaaaaaaaaa*aaa\"; return __memchr(s, 42, 0) + 22; }", 42},
		{"memchr-v128-miss", "function main(): i32 { var s: string = \"aaaaaaaaaaaaaaaaaaaaaaaa\"; return __memchr(s, 42, 0) + 43; }", 42},
		{"ascii-run-v128", "function main(): i32 { var s: string = \"aaaaaaaaaaaaaaaaaaaaaaaa\"; return __ascii_run(s, 0) + 18; }", 42},
		// random_i32(): a single i32 of randomness. Used in self-cancelling
		// arithmetic so the result is deterministic (42) while still
		// exercising the builtin's call + helper emission end-to-end.
		{"random-i32", "function main(): i32 { var r: i32 = random_i32(); return (r - r) + 42; }", 42},
		// putchar(c): write c's low byte to stdout. Exits 0; the binary and
		// WAT paths must agree on the emitted bytes ("Hi\n").
		{"putchar", "function main(): i32 { putchar(72); putchar(105); putchar(10); return 0; }", 0},
		// eprint(s): write to stderr (preview1 fd 2). stdout stays "x" (the
		// stderr write must be a valid call and must not corrupt stdout).
		{"eprint", "function main(): i32 { eprint(\"log\"); write(\"x\"); return 0; }", 0},
		// exit(code): preview1 proc_exit. exit(0) terminates early -> stdout
		// "a" only (the "b" after exit is unreachable), exit code 0.
		{"exit", "function main(): i32 { write(\"a\"); exit(0); write(\"b\"); return 0; }", 0},
		// A VALUE-PRODUCING flat block — `if (result i32)`, which is what
		// emit_dyn_dispatch writes. The encoder hardcoded the empty blocktype
		// 0x40 for every flat block/loop/if and the header readers counted the
		// `(result i32)` node as a second FUNCTION result, so the module the
		// WAT path ran fine as text failed to validate once assembled: the
		// declared type grew a phantom result and the arms' values had nowhere
		// to go (#6906). (3+1) + (4+1) + (14+1) = 24.
		{"dyn-dispatch-blocktype", "trait Sh { function area(self: Self): i32; } impl Sh for i32 { function area(self: Self): i32 { return self + 1; } } function total(xs: dyn Sh[]): i32 { var t: i32 = 0; for x in xs { t = t + x.area(); } return t; } function main(): i32 { return total([3, 4, 14]); }", 24},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. WAT + its reference exit / stdout.
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

			// 2. Assemble: run the (prebuilt) assembler over target.wat.
			out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
			if err != nil {
				// Surface everything a CI-only failure needs: the
				// assembler's stderr (wasmtime prints trap/validation
				// detail there) plus size + hash of both inputs, so a
				// local repro can confirm it is chewing the same bytes.
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

			// 2b. For the SIMD cases, disassemble with an EXTERNAL tool and
			// check the mnemonics came back. Agreeing exit codes would catch
			// most encoding slips, but not the one ATLAS-PLATFORM-PLAN §3.3a
			// names: the 0xFD sub-opcode space is dense uleb128, so a wrong
			// byte is usually another VALID instruction — the module still
			// validates, still runs, and only a disassembler can say which
			// instruction was actually emitted. wasm-tools is that oracle;
			// a typed table cannot be its own.
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

			// 3. Run the binary module; assert exit + stdout match the WAT path.
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

// wantSIMDMnemonics names, per case, the v128 instructions watbin must have
// emitted — checked by disassembling its output rather than by pinning bytes,
// for the reason at the call site. i32.ctz is in the list because it is what
// turns i8x16.bitmask's result into a lane index, and a kernel that lost it
// would still validate.
var wantSIMDMnemonics = map[string][]string{
	"memchr-v128":      {"i8x16.splat", "v128.load", "i8x16.eq", "i8x16.bitmask", "i32.ctz"},
	"memchr-v128-miss": {"i8x16.splat", "v128.load", "i8x16.eq", "i8x16.bitmask", "i32.ctz"},
	"ascii-run-v128":   {"v128.load", "i8x16.bitmask", "i32.ctz"},
}

// asmReadFileDriver is the assembler's entry point: read the target WAT
// from the preopened dir, assemble it, and print the module bytes as
// newline-separated decimals (the test reassembles them into a .wasm).
const asmReadFileDriver = `
function main(): i32 {
    match (read_file("target.wat")) {
        Ok(wat) => {
            var bytes: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var i: i32 = 0;
            while (i < bytes.len()) { print_int(bytes[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { eprint("assembler: read_file(target.wat) failed"); return 1; }
    }
    return 2;
}
`
