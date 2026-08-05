package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The wasm half of #6149's static aggregate constants (slice 2b). Where the
// register backends emit a `.K<n>` label in `.data`, wasm gets a REGION of
// linear memory above the string literals, based at `$__cagg_base<ns>` — the
// sibling of `$__str_base<ns>` — with each unit's code emitting relative
// offsets.
//
// Two things are worth pinning beyond the values:
//
//   - The region is a LAYOUT change, not just an emit arm: its size feeds
//     fl_base / heap_base, and getting those wrong points the freelist head
//     array at live constants rather than failing loudly. `$heap` is asserted
//     to sit above the last constant block for exactly that reason.
//   - Immortality is free on wasm and must stay that way. The rc guard chain
//     ($__fern_rc_inc / $__fern_arr_dec / $__fern_rc_is_unique) returns early
//     for any pointer below heap_base, and the whole region is below it — so a
//     record update or FBIP reuse targeting a constant degrades to a fresh box
//     instead of writing through the shared block. `update-does-not-write-
//     through` and `churn-does-not-corrupt` read WRONG, not merely slow, if
//     that stops holding.
//
// Exit codes stay under 126: WASI refuses anything outside [0..126), so a
// larger value surfaces as wasmtime's exit 1 and reads as a phantom mismatch.
var constAggWasmCases = []struct {
	name    string
	src     string
	exit    int
	blocks  int  // `(data …)` segments in the constant region
	noBoxes bool // no $__fern_str_box call in the emitted user functions
}{
	// The idiom: three nullary constructors, three interned blocks, and not one
	// allocation among them.
	{"nullary-ctors",
		`struct TypeI32 { is_char: boolean, width: i32, unsigned: boolean } struct TypeString { tag: i32 } type Type = TypeI32 | TypeString; function t_i32(): Type { return TypeI32 { is_char: false, width: 32, unsigned: false }; } function t_u64(): Type { return TypeI32 { is_char: false, width: 64, unsigned: true }; } function t_str(): Type { return TypeString { tag: 0 }; } function w(t: Type): i32 { match (t) { TypeI32(v) => { return v.width; }, TypeString(_) => { return 7; } } return 0; } function main(): i32 { var a: Type = t_i32(); var b: Type = t_u64(); var c: Type = t_str(); return (w(a) + w(b) + w(c)) % 200; }`,
		103, 3, true},
	// INTERNING: the same literal from two constructors is ONE block.
	{"identical-literals-intern",
		`struct P { a: i32, b: i32 } function one(): P { return P { a: 1, b: 2 }; } function two(): P { return P { a: 1, b: 2 }; } function three(): P { return P { a: 9, b: 9 }; } function main(): i32 { return (one().a + two().b + three().a) % 200; }`,
		12, 2, true},
	// SOUNDNESS: a record update whose base is a constant must not write through
	// the shared block — `r` reads it again afterwards and must still see a=5.
	{"update-does-not-write-through",
		`struct P { a: i32, b: boolean } function mk(): P { return P { a: 5, b: true }; } function main(): i32 { var p: P = mk(); var q: P = P { ...p, a: p.a + 1 }; var r: P = mk(); var t: i32 = 0; if (p.b) { t = t + 1; } if (q.b) { t = t + 2; } return (p.a * 10 + q.a + r.a + t) % 200; }`,
		64, 1, false},
	// SOUNDNESS at scale: 100k updates threaded off a constant, then the
	// constant is read again. One write-through corrupts `fresh`. The exit code
	// folds p.a into WASI's range: (p.a % 100) + fresh.a * 10 + fresh.b = 93 + 12.
	{"churn-does-not-corrupt",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 1, b: 2 }; } function main(): i32 { var p: P = mk(); var i: i32 = 0; while (i < 100000) { p = P { ...p, a: (p.a + p.b) % 977 }; i = i + 1; } var fresh: P = mk(); return (p.a % 100) + fresh.a * 10 + fresh.b; }`,
		105, 1, false},
	// A constant moved into a container: the array owns a pointer to the shared
	// block, and the exit sweep must not free it — here the low-address guard is
	// what stops $__fern_arr_dec_ptr from pushing a data-section address onto a
	// freelist, which would hand it back as a "fresh" box on the next alloc.
	{"constant-into-container",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 5, b: 9 }; } function main(): i32 { var ps: P[] = []; var i: i32 = 0; while (i < 4) { ps = ps.append(mk()); i = i + 1; } var s: i32 = 0; var j: i32 = 0; while (j < ps.len()) { s = s + ps[j].a + ps[j].b; j = j + 1; } return s % 200; }`,
		56, 1, false},
	// A negative field literal: written sign-extended across the whole 8-byte
	// slot. Division-based byte extraction could not render it (`-1 / 256 % 256`
	// does not name a byte), which is why le32_escape shifts and masks.
	{"negative-field-literal",
		`struct N { v: i32, f: boolean } function neg(): N { return N { v: -7, f: true }; } function main(): i32 { var a: N = neg(); var t: i32 = 0; if (a.f) { t = t + 100; } return t + a.v; }`,
		93, 1, true},
	// A hex field literal: the packed word carries SOURCE TEXT, so it reaches
	// the data segment intact rather than being zeroed by a decimal-only parse.
	{"hex-field-literal",
		`struct N { v: i32, f: boolean } function hx(): N { return N { v: 0x1f, f: false }; } function main(): i32 { var a: N = hx(); var t: i32 = 0; if (a.f) { t = t + 100; } return t + a.v; }`,
		31, 1, true},
	// The constant/REUSE interaction: two same-block literals where the second
	// would otherwise reuse the first's dead box. Both reach static placement,
	// because `reuse_recipient_ok` excludes a recipient that is itself constant —
	// the reuse scanners run per STATEMENT in lower_block, before the literal
	// reaches lower_expr where the constant is recognised, so a claimed recipient
	// silently loses its placement. Shared with the x86-64 suite, which reads the
	// same shape through the allocation counter.
	{"reuse-shape-all-constant",
		`struct P { x: i32, y: i32 } function main(): i32 { var cond: i32 = 1; var r: i32 = 0; if (cond > 0) { var a: P = P { x: 10, y: 20 }; var s: i32 = a.x + a.y; var b: P = P { x: 3, y: 4 }; r = s + b.x + b.y; } return r; }`,
		37, 2, true},
	// NOT admitted: `0 - 3` is a binary expression, not a unary minus, so the
	// literal stays on struct_make and no constant is emitted at all.
	{"non-literal-field-not-admitted",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 0 - 3, b: 0x10 }; } function main(): i32 { var p: P = mk(); return (p.a + p.b + 100) % 200; }`,
		113, 0, false},
	// NOT admitted: a wide (i64) field. The block writes one 8-byte slot per
	// field but the admission classifier only vouches for i32-width values.
	{"wide-field-not-admitted",
		`struct W { n: i64, k: i32 } function mk(): W { return W { n: 5000000000, k: 3 }; } function main(): i32 { var w: W = mk(); if (w.n > 4000000000) { return w.k + 10; } return 0; }`,
		13, 0, false},
}

// TestSelfHostConstAggregateWasmIR compiles each case through the self-hosted
// wasm IR driver and asserts the emitted region shape, the placement of `$heap`
// above it, and the runtime exit code under wasmtime.
func TestSelfHostConstAggregateWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host const-aggregate wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range constAggWasmCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			out, err := cmd.Output()
			if err != nil || len(out) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			wat := string(out)

			base, blocks, ok := constAggRegion(wat)
			if !ok && tc.blocks > 0 {
				t.Fatalf("%s: no $__cagg_base global emitted, want %d constant blocks", tc.name, tc.blocks)
			}
			if len(blocks) != tc.blocks {
				t.Errorf("%s: %d constant blocks emitted, want %d — the const_struct admission moved, or interning stopped collapsing identical literals", tc.name, len(blocks), tc.blocks)
			}
			// $heap must sit above the whole region. If it does not, the freelist
			// head array overlaps live constants and allocation hands them back.
			if tc.blocks > 0 {
				if heap, hok := watGlobalConst(wat, "$heap"); hok {
					if heap <= base {
						t.Errorf("%s: $heap = %d is not above the constant region base %d — freelist/heap bases did not follow the region", tc.name, heap, base)
					}
				}
			}
			// A placed constant means the user code does not allocate its box.
			// $__fern_str_box is still DEFINED (the runtime always is), so look
			// only past the runtime, at the emitted user functions.
			if tc.noBoxes && strings.Contains(userFuncsOf(wat), "call $__fern_str_box") {
				t.Errorf("%s: user code still calls $__fern_str_box — a constant fell back to struct_make", tc.name)
			}

			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, out, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}

// constAggRegion returns the constant region's base address and the data
// segments that fall inside it. Segments are matched by ADDRESS rather than by
// order, so a string literal emitted alongside cannot be miscounted as a
// constant.
func constAggRegion(wat string) (int, []int, bool) {
	base, ok := watGlobalConst(wat, "$__cagg_base")
	if !ok {
		return 0, nil, false
	}
	var blocks []int
	for _, line := range strings.Split(wat, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "(data (i32.const ") {
			continue
		}
		rest := strings.TrimPrefix(line, "(data (i32.const ")
		end := strings.Index(rest, ")")
		if end < 0 {
			continue
		}
		addr, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
		if err != nil || addr < base {
			continue
		}
		blocks = append(blocks, addr)
	}
	return base, blocks, true
}

// watGlobalConst reads the constant initialiser of `(global <name> i32 (i32.const N))`.
func watGlobalConst(wat, name string) (int, bool) {
	for _, line := range strings.Split(wat, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "(global "+name+" ") {
			continue
		}
		i := strings.LastIndex(line, "(i32.const ")
		if i < 0 {
			continue
		}
		rest := line[i+len("(i32.const "):]
		end := strings.Index(rest, ")")
		if end < 0 {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimSpace(rest[:end])); err == nil {
			return v, true
		}
	}
	return 0, false
}

// userFuncsOf returns the WAT from the first non-runtime function on. Every
// runtime helper is named `$__…`, so the first `(func $x` whose name is not is
// where the program's own code begins.
func userFuncsOf(wat string) string {
	lines := strings.Split(wat, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "(func $") && !strings.HasPrefix(t, "(func $__") {
			return strings.Join(lines[i:], "\n")
		}
	}
	return ""
}
