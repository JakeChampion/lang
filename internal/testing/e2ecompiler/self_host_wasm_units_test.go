package e2ecompiler

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostWasmUnitsN2(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm N=2 unit-link e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm N=2 unit-link e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_run.fern", "drivers/wasm_units_probe.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_units_probe.fern", "wasm_units_probe")
	wholeBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "fern_runtime_string_order",
			src: `function less(a: string, b: string): boolean { return a < b; }
function greater(a: string, b: string): boolean { return a > b; }
function main(): i32 {
    let values: string[] = ["", "a", "aa", "ab", "b", "é", "界", "𐐀"];
    let i: i32 = 0;
    while (i < values.len()) {
        let j: i32 = 0;
        while (j < values.len()) {
            if (less(values[i], values[j]) != (i < j)) { return 1; }
            if (greater(values[i], values[j]) != (i > j)) { return 2; }
            if ((values[i] <= values[j]) != (i <= j)) { return 3; }
            if ((values[i] >= values[j]) != (i >= j)) { return 4; }
            if ((values[i] == values[j]) != (i == j)) { return 6; }
            if ((values[i] != values[j]) != (i != j)) { return 7; }
            j = j + 1;
        }
        i = i + 1;
    }
    let allocated: string = values[1] + values[3];
    if (!less(allocated, values[4]) || !greater(allocated, values[1])) { return 5; }
    if (allocated != "aab") { return 8; }
    return 0;
}`,
			want: 0,
		},
		{
			name: "utf8_runtime_step",
			src: `function widths(s: string, want: i32[]): boolean {
    let parts: string[] = s.split("");
    if (parts.len() != want.len()) { return false; }
    let i: i32 = 0; let at: i32 = 0;
    while (i < parts.len()) {
        if (parts[i].len() != want[i]) { return false; }
        let j: i32 = 0;
        while (j < parts[i].len()) {
            if (parts[i][j] != s[at]) { return false; }
            at = at + 1; j = j + 1;
        }
        i = i + 1;
    }
    return at == s.len();
}
function main(): i32 {
    if (!widths("", []) || !widths("aé界😀", [1, 2, 3, 4])) { return 1; }
    if (!widths(string_from_bytes_unchecked([0 as u8, 128 as u8, 192 as u8, 193 as u8, 245 as u8, 255 as u8]), [1, 1, 1, 1, 1, 1])) { return 2; }
    if (!widths(string_from_bytes_unchecked([194 as u8, 128 as u8, 223 as u8, 191 as u8]), [2, 2])) { return 3; }
    if (!widths(string_from_bytes_unchecked([224 as u8, 160 as u8, 128 as u8, 237 as u8, 159 as u8, 191 as u8]), [3, 3])) { return 4; }
    if (!widths(string_from_bytes_unchecked([240 as u8, 144 as u8, 128 as u8, 128 as u8, 244 as u8, 143 as u8, 191 as u8, 191 as u8]), [4, 4])) { return 5; }
    if (!widths(string_from_bytes_unchecked([224 as u8, 159 as u8, 128 as u8]), [1, 1, 1])) { return 6; }
    if (!widths(string_from_bytes_unchecked([237 as u8, 160 as u8, 128 as u8]), [1, 1, 1])) { return 7; }
    if (!widths(string_from_bytes_unchecked([240 as u8, 143 as u8, 128 as u8, 128 as u8]), [1, 1, 1, 1])) { return 8; }
    if (!widths(string_from_bytes_unchecked([244 as u8, 144 as u8, 128 as u8, 128 as u8]), [1, 1, 1, 1])) { return 9; }
    if (!widths(string_from_bytes_unchecked([194 as u8]), [1])) { return 10; }
    if (!widths(string_from_bytes_unchecked([225 as u8, 128 as u8]), [2])) { return 11; }
    if (!widths(string_from_bytes_unchecked([241 as u8, 128 as u8, 128 as u8]), [3])) { return 12; }
    if (!widths(string_from_bytes_unchecked([225 as u8, 128 as u8, 32 as u8]), [2, 1])) { return 13; }
    if (!widths(string_from_bytes_unchecked([241 as u8, 128 as u8, 32 as u8]), [2, 1])) { return 14; }
    return 0;
}`,
			want: 0,
		},
		{
			name: "utf8_runtime_lines",
			src: `function rows(s: string): string[] { return s.lines(); }
function main(): i32 {
    let a: string[] = rows("a\r\nbb\n");
    if (a.len() != 2 || a[0] != "a" || a[1] != "bb") { return 1; }
    if (rows("").len() != 0 || rows("x").len() != 1) { return 2; }
    return 0;
}`,
			want: 0,
		},
		{
			name: "fern_runtime_memchr_bytes",
			src:  e2eharness.MemchrBytesSource(true),
			want: 0,
		},
		{
			name: "fern_runtime_rmemchr_bytes",
			src:  e2eharness.RmemchrBytesSource(true),
			want: 0,
		},
		{
			name: "fern_runtime_count_byte_bytes",
			src:  e2eharness.CountByteBytesSource(true),
			want: 0,
		},
		{
			name: "fern_runtime_mulhi",
			src:  wasmRuntimeMulhiProgram(),
			want: 0,
		},
		{
			name: "fern_runtime_set_scans_bytes",
			src:  e2eharness.ByteSetScansSource(true),
			want: 0,
		},
		{
			name: "fern_runtime_set_scans_string",
			src:  wasmRuntimeStringSetScansProgram(),
			want: 0,
		},
		{
			name: "fern_runtime_byte_reductions",
			src:  wasmRuntimeByteReductionsProgram(true),
			want: 0,
		},
		{
			name: "fern_runtime_string_reductions",
			src:  wasmRuntimeByteReductionsProgram(false),
			want: 0,
		},
		{
			name: "conditional_branches",
			src: `function sum(n: i32): i32 {
    if (n < 0) { return -10; }
    let out: i32 = 0; let i: i32 = 0;
    while (i < n) {
        if (i == 2) { i = i + 1; continue; }
        if (i == 5) { break; }
        if (i == 3) { out = out + 3; } else { out = out + i; }
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    if (sum(-1) != -10 || sum(0) != 0 || sum(2) != 1 || sum(8) != 8) { return 1; }
    return 0;
}`,
			want: 0,
		},
		{
			name: "buffer_needs_from_both_units",
			src: `function create(): usize { return buf_new(1); }
function text_part(h: usize): void { buf_push(h, "a"); }
function byte_part(h: usize): void { let bytes: u8[] = [255 as u8]; buf_push_bytes_range(h, bytes, 0, 1); }
function finish(h: usize): u8[] { let bytes = buf_take_bytes(h); buf_free(h); return bytes; }
function main(): i32 {
    let h = create(); text_part(h); byte_part(h); let bytes = finish(h);
    if (bytes.len() == 2 && bytes[0] == 97 as u8 && bytes[1] == 255 as u8) { return 0; }
    return 1;
}`,
			want: 0,
		},
		{
			name: "cross_unit_calls_both_directions",
			src: `function a(n: i32): i32 { return b(n) + 1; }
function b(n: i32): i32 { return c(n) * 2; }
function c(n: i32): i32 { return n + 3; }
function d(n: i32): i32 { return a(n) - 1; }
function main(): i32 { return d(4); }`,
			want: 14,
		},
		{
			name: "literals_owned_by_different_halves",
			src: `function first(): string { return "alpha"; }
function second(): string { return "beta"; }
function third(): string { return "gamma-longer"; }
function fourth(): i32 { return first().len() + second().len(); }
function main(): i32 { return fourth() + third().len(); }`,
			want: 21,
		},
		{
			name: "fn_value_taken_in_one_half_called_in_another",
			src: `function add10(n: i32): i32 { return n + 10; }
function twice(n: i32): i32 { return n * 2; }
function apply(f: (i32) => i32, n: i32): i32 { return f(n); }
function useit(): i32 { return apply(add10, 5) + apply(twice, 3); }
function main(): i32 { return useit(); }`,
			want: 21,
		},
		{
			name: "instance_record_with_a_wide_field",
			src: `struct Slot[T] { v: T }
function keep[T](f: () => T): T { let c: Slot[T] = Slot[T] { v: f() }; return c.v; }
function wide(): i64 { let fs: (() => i64)[] = [(): i64 => 5000000010 as i64]; return keep(fs[0]); }
function main(): i32 { let gs: (() => string)[] = [(): string => "ab" + "c"]; return ((wide() - (5000000000 as i64)) as i32) + keep(gs[0]).len(); }`,
			want: 13,
		},
	}

	build := func(t *testing.T, bin, tag, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("%s: driver failed: %v\n%s", tag, err, stderr.String())
		}
		if strings.HasPrefix(tag, "fern_runtime_string_order") {
			if strings.Count(string(wat), "(func $__fern_strcmp ") != 1 {
				t.Fatal("the module must define one string comparator")
			}
			if !strings.Contains(string(wat), "(func $__fern_strcmp (param i32)") {
				t.Fatal("the string comparator does not use the Fern function emitter")
			}
		} else if strings.Contains(string(wat), "(func $__fern_strcmp ") {
			t.Fatal("a module without string ordering contains its runtime helper")
		}

		if strings.Contains(string(wat), "(func $__fern_streq ") && !strings.Contains(string(wat), "(func $__fern_streq (param i32)") {
			t.Fatal("string equality does not use the Fern function emitter")
		}

		if strings.HasPrefix(tag, "utf8_runtime_") {
			if strings.Count(string(wat), "(func $__fern_utf8_step ") != 1 || !strings.Contains(string(wat), "(func $__fern_utf8_step (param i32)") {
				t.Fatal("UTF-8 stepping does not use one ordinary Fern function")
			}
		} else if strings.Contains(string(wat), "(func $__fern_utf8_step ") {
			t.Fatal("a module without split or lines contains its UTF-8 runtime helper")
		}

		for _, name := range []string{"memchr_bytes", "rmemchr_bytes"} {
			symbol := "(func $__fern_" + name + " "
			if strings.HasPrefix(tag, "fern_runtime_"+name) {
				if strings.Count(string(wat), symbol) != 1 || !strings.Contains(string(wat), symbol+"(param i32)") {
					t.Fatalf("%s does not use one ordinary Fern function", name)
				}
			} else if strings.Contains(string(wat), symbol) {
				t.Fatalf("an unrelated module contains the %s runtime helper", name)
			}
		}

		if strings.HasPrefix(tag, "fern_runtime_count_byte_bytes") {
			if strings.Count(string(wat), "(func $__fern_count_byte_bytes ") != 1 || !strings.Contains(string(wat), "(func $__fern_count_byte_bytes (param i32) (param i32) (result i32)") {
				t.Fatal("byte-array counting does not use one ordinary Fern function")
			}
		} else if strings.Contains(string(wat), "(func $__fern_count_byte_bytes ") {
			t.Fatal("an unrelated module contains the byte-array counting helper")
		}

		if strings.HasPrefix(tag, "fern_runtime_mulhi") {
			if strings.Count(string(wat), "(func $__fern_mulhi_u64 ") != 1 || !strings.Contains(string(wat), "(func $__fern_mulhi_u64 (param i64) (param i64) (result i64)") {
				t.Fatal("high multiplication does not use one ordinary Fern function with the i64 ABI")
			}
		} else if strings.Contains(string(wat), "(func $__fern_mulhi_u64 ") {
			t.Fatal("an unrelated module contains the high multiplication runtime helper")
		}

		for _, name := range []string{"scan_set", "count_runs", "scan_set_bytes", "count_runs_bytes"} {
			symbol := "(func $__fern_" + name + " "
			kind := "string"
			if strings.HasSuffix(name, "_bytes") {
				kind = "bytes"
			}
			if strings.HasPrefix(tag, "fern_runtime_set_scans_"+kind) {
				if strings.Count(string(wat), symbol) != 1 || !strings.Contains(string(wat), symbol+"(param i32)") {
					t.Fatalf("%s does not use one ordinary Fern function", name)
				}
			} else if strings.Contains(string(wat), symbol) {
				t.Fatalf("an unrelated module contains the %s runtime helper", name)
			}
		}

		for _, name := range []string{"sum_bytes_array", "bsd_sum_bytes", "sum_bytes", "bsd_sum"} {
			symbol := "(func $__fern_" + name + " "
			kind := "string"
			if name == "sum_bytes_array" || name == "bsd_sum_bytes" {
				kind = "byte"
			}
			// The byte-view reader retains its string arm.
			wanted := strings.HasPrefix(tag, "fern_runtime_"+kind+"_reductions") || kind == "string" && strings.HasPrefix(tag, "fern_runtime_byte_reductions")
			if wanted {
				if strings.Count(string(wat), symbol) != 1 || !strings.Contains(string(wat), symbol+"(param i32)") {
					t.Fatalf("%s does not use one ordinary Fern function", name)
				}
			} else if strings.Contains(string(wat), symbol) {
				t.Fatalf("an unrelated module contains the %s runtime helper", name)
			}
		}

		if strings.HasPrefix(tag, "fern_runtime_string_reductions") {
			end := bytes.LastIndexByte(wat, ')')
			wat = append(append(append([]byte{}, wat[:end]...), []byte(wasmRuntimeStringViewProbe())...), wat[end:]...)
		}

		watPath := filepath.Join(dir, tag+".wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		corePath := filepath.Join(dir, tag+".wasm")
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
			t.Fatalf("%s: wasm-tools parse: %v\n%s", tag, err, out)
		}
		out, err := exec.Command(wasmtime, "run", corePath).CombinedOutput()
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("%s: wasmtime run: %v\n%s", tag, err, out)
			}
			return ee.ExitCode()
		}
		if strings.HasPrefix(tag, "fern_runtime_string_reductions") {
			invoke := exec.Command(wasmtime, "run", "--invoke", "runtime-view-check", corePath)
			var diagnostics bytes.Buffer
			invoke.Stderr = &diagnostics
			out, err := invoke.Output()
			if err != nil || strings.TrimSpace(string(out)) != "0" {
				t.Fatalf("%s: raw view forms: %v\n%s\n%s", tag, err, out, diagnostics.String())
			}
		}
		return 0
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			whole := build(t, wholeBin, tc.name+"_whole", tc.src)
			if whole != tc.want {
				t.Fatalf("whole-program build returned %d, want %d — the oracle itself is wrong, so the N=2 comparison below would be meaningless", whole, tc.want)
			}
			split := build(t, probeBin, tc.name+"_split", tc.src)
			if split != tc.want {
				t.Errorf("two-unit build returned %d, want %d (whole-program build agrees at %d)", split, tc.want, whole)
			}
		})
	}

	t.Run("emits_two_namespaced_units", func(t *testing.T) {
		wat, err := os.ReadFile(filepath.Join(dir, "literals_owned_by_different_halves_split.wat"))
		if err != nil {
			t.Fatalf("read split wat: %v", err)
		}
		if !strings.Contains(string(wat), "$__str_base$u1") {
			t.Error("split WAT declares no $__str_base$u1 — the probe emitted a single unit, so the multi-unit path was never exercised")
		}
	})
}

func wasmRuntimeMulhiProgram() string {
	values := []uint64{0, 1, ^uint64(0), 1 << 63, 1 << 32, (1 << 32) - 1, 0xaaaaaaaaaaaaaaaa, 0x5555555555555555}
	for bit := range 64 {
		values = append(values, uint64(1)<<bit, ^(uint64(1) << bit))
	}
	var src strings.Builder
	src.WriteString("@noinline function high(a: u64, b: u64): u64 { return __mulhi_u64(a, b); }\nfunction main(): i32 {\n")
	for i, a := range values {
		b := values[len(values)-1-i]
		hi, _ := bits.Mul64(a, b)
		fmt.Fprintf(&src, "if (high(%du64, %du64) != %du64) { return 1; }\n", a, b, hi)
		hi, _ = bits.Mul64(a, 0xa2f9836e4e441529)
		fmt.Fprintf(&src, "if (high(%du64, 11743562013128004905u64) != %du64) { return 2; }\n", a, hi)
	}
	src.WriteString("return 0;\n}\n")
	return src.String()
}

func wasmRuntimeStringSetScansProgram() string {
	src := e2eharness.ByteSetScansSource(false)
	src = strings.Replace(src, "fip function scan(", "function scan(", 1)
	src = strings.Replace(src, "fip function runs(", "function runs(", 1)
	src = strings.Replace(src, "return __scan_set_bytes(bytes, from, set);", "return __scan_set(string_from_bytes_unchecked(bytes), from, set);", 1)
	src = strings.Replace(src, "return __count_runs_bytes(bytes, inside, set);", "return __count_runs(string_from_bytes_unchecked(bytes), inside, set);", 1)
	probe := `let text = string_from_bytes_unchecked(all);
    let before = __heap_alloc_count();
    let repeat = 0;
    while (repeat < 1000) {
        if (__scan_set(text, 0, all) != 1 || __count_runs(text, 0, all) != 1) { return 10; }
        repeat = repeat + 1;
    }
    if (__heap_alloc_count() != before) { return 11; }
    `
	return strings.Replace(src, "let set: u8[] = __alloc_u8(256);", probe+"let set: u8[] = __alloc_u8(256);", 1)
}

func wasmRuntimeByteReductionsProgram(array bool) string {
	var src strings.Builder
	src.WriteString(`@noinline function sum(a: u8[]): i32 { return __sum_bytes_array(a); }
@noinline function bsd(a: u8[], seed: i32): i32 { return __bsd_sum_bytes(a, seed); }
function prefix(a: u8[], n: i32): u8[] {
    let out: u8[] = []; let i: i32 = 0;
    while (i < n) { out = out.append(a[i]); i = i + 1; }
    return out;
}
function main(): i32 {
let all: u8[] = [`)
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i * 73)
		fmt.Fprintf(&src, "%du8,", data[i])
	}
	src.WriteString("];\n")
	for _, n := range []int{0, 1, 2, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 255, 256} {
		fmt.Fprintf(&src, "{ let a: u8[] = prefix(all, %d);\n", n)
		var sum uint32
		for _, b := range data[:n] {
			sum += uint32(b)
		}
		fmt.Fprintf(&src, "if (sum(a) != %d) { return 1; }\n", sum)
		for _, seed := range []int32{0, 1, 65535, 65536, 65537, -1, 2147483647, -2147483648} {
			total := uint32(seed) & 65535
			for _, b := range data[:n] {
				total = (((total >> 1) | (total << 15)) + uint32(b)) & 65535
			}
			seedText := fmt.Sprint(seed)
			if seed < 0 {
				seedText = fmt.Sprintf("0 - %d - 1", -int64(seed)-1)
			}
			fmt.Fprintf(&src, "if (bsd(a, %s) != %d) { return 2; }\n", seedText, total)
		}
		src.WriteString("}\n")
	}
	src.WriteString(`let before = __heap_alloc_count(); let repeat: i32 = 0;
while (repeat < 1000) {
    if (sum(all) != 32640 || bsd(all, 65536) != bsd(all, 0)) { return 3; }
    repeat = repeat + 1;
}
if (__heap_alloc_count() != before) { return 4; }
let held = all; all = all.with(0, 255u8);
if (sum(held) != 32640 || sum(all) != 32895) { return 5; }
let h = buf_new(16843010); let chunk: u8[] = __alloc_u8(65536);
let i: i32 = 0;
while (i < chunk.len()) { chunk = chunk.with(i, 255u8); i = i + 1; }
i = 0;
while (i < 257) { buf_push_bytes_range(h, chunk, 0, chunk.len()); i = i + 1; }
buf_push_bytes_range(h, chunk, 0, 258);
let large = buf_take_bytes(h); buf_free(h);
if (large.len() != 16843010 || sum(large) != 254) { return 6; }
let ph = buf_new(8421505); buf_push_bytes_range(ph, large, 0, 8421505);
let part = buf_take_bytes(ph); buf_free(ph);
if (sum(part) != 0 - 2147483521) { return 7; }
return 0;
}`)
	program := src.String()
	if !array {
		program = strings.Replace(program, "__sum_bytes_array(a)", "__sum_bytes(string_from_bytes_unchecked(a))", 1)
		program = strings.Replace(program, "__bsd_sum_bytes(a, seed)", "__bsd_sum(string_from_bytes_unchecked(a), seed)", 1)
		program = strings.Replace(program, "let before =", "let text = string_from_bytes_unchecked(all); let before =", 1)
		program = strings.Replace(program, "sum(all) != 32640 || bsd(all, 65536) != bsd(all, 0)", "__sum_bytes(text) != 32640 || __bsd_sum(text, 65536) != __bsd_sum(text, 0)", 1)
		program = strings.Replace(program, "let before =", "let view: str = slice_unchecked(text, 1, 255); if (__sum_bytes(view) != __sum_bytes(text) - 183) { return 8; } let before =", 1)
	}
	return program
}

func wasmRuntimeStringViewProbe() string {
	var wat strings.Builder
	wat.WriteString(`
  (func (export "runtime-view-check") (result i32) (local $p i32)
    (local.set $p (i32.shl (memory.grow (i32.const 1)) (i32.const 16)))
    (i32.store (local.get $p) (i32.const 3))
    (i32.store8 offset=4 (local.get $p) (i32.const 0))
    (i32.store8 offset=5 (local.get $p) (i32.const 128))
    (i32.store8 offset=6 (local.get $p) (i32.const 255))
    (i32.store offset=32 (local.get $p) (i32.const -2147483645))
    (i32.store offset=36 (local.get $p) (i32.add (local.get $p) (i32.const 4)))
    (i32.store offset=48 (local.get $p) (i32.add (local.get $p) (i32.const 4)))
    (i32.store offset=52 (local.get $p) (i32.const 3))
`)
	for _, offset := range []int{0, 32, 49} {
		fmt.Fprintf(&wat, "    (if (i32.ne (call $__fern_sum_bytes (i32.add (local.get $p) (i32.const %d))) (i32.const 383)) (then (return (i32.const 1))))\n", offset)
		fmt.Fprintf(&wat, "    (if (i32.ne (call $__fern_bsd_sum (i32.add (local.get $p) (i32.const %d)) (i32.const 65537)) (i32.const 8511)) (then (return (i32.const 2))))\n", offset)
	}
	wat.WriteString(`    (i32.store offset=32 (local.get $p) (i32.const -2147483648))
    (i32.store offset=36 (local.get $p) (i32.const -1))
    (i32.store offset=48 (local.get $p) (i32.const -1))
    (i32.store offset=52 (local.get $p) (i32.const 0))
`)
	for _, offset := range []int{32, 49} {
		fmt.Fprintf(&wat, "    (if (call $__fern_sum_bytes (i32.add (local.get $p) (i32.const %d))) (then (return (i32.const 3))))\n", offset)
		fmt.Fprintf(&wat, "    (if (i32.ne (call $__fern_bsd_sum (i32.add (local.get $p) (i32.const %d)) (i32.const -1)) (i32.const 65535)) (then (return (i32.const 4))))\n", offset)
	}
	wat.WriteString("    (i32.const 0))\n")
	return wat.String()
}
