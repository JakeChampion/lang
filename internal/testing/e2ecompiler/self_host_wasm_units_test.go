package e2ecompiler

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
