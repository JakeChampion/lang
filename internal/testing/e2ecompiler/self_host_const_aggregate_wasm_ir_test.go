package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var constAggWasmCases = []struct {
	name    string
	src     string
	exit    int
	blocks  int
	noBoxes bool
}{
	{"nullary-ctors",
		`struct TypeI32 { is_char: boolean, width: i32, unsigned: boolean } struct TypeString { tag: i32 } type Type = TypeI32 | TypeString; function t_i32(): Type { return TypeI32 { is_char: false, width: 32, unsigned: false }; } function t_u64(): Type { return TypeI32 { is_char: false, width: 64, unsigned: true }; } function t_str(): Type { return TypeString { tag: 0 }; } function w(t: Type): i32 { match (t) { TypeI32(v) => { return v.width; }, TypeString(_) => { return 7; } } return 0; } function main(): i32 { let a: Type = t_i32(); let b: Type = t_u64(); let c: Type = t_str(); return (w(a) + w(b) + w(c)) % 200; }`,
		103, 3, true},
	{"identical-literals-intern",
		`struct P { a: i32, b: i32 } function one(): P { return P { a: 1, b: 2 }; } function two(): P { return P { a: 1, b: 2 }; } function three(): P { return P { a: 9, b: 9 }; } function main(): i32 { return (one().a + two().b + three().a) % 200; }`,
		12, 2, true},
	{"update-does-not-write-through",
		`struct P { a: i32, b: boolean } function mk(): P { return P { a: 5, b: true }; } function main(): i32 { let p: P = mk(); let q: P = P { ...p, a: p.a + 1 }; let r: P = mk(); let t: i32 = 0; if (p.b) { t = t + 1; } if (q.b) { t = t + 2; } return (p.a * 10 + q.a + r.a + t) % 200; }`,
		64, 1, false},
	{"churn-does-not-corrupt",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 1, b: 2 }; } function main(): i32 { let p: P = mk(); let i: i32 = 0; while (i < 100000) { p = P { ...p, a: (p.a + p.b) % 977 }; i = i + 1; } let fresh: P = mk(); return (p.a % 100) + fresh.a * 10 + fresh.b; }`,
		105, 1, false},
	{"constant-into-container",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 5, b: 9 }; } function main(): i32 { let ps: P[] = []; let i: i32 = 0; while (i < 4) { ps = ps.append(mk()); i = i + 1; } let s: i32 = 0; let j: i32 = 0; while (j < ps.len()) { s = s + ps[j].a + ps[j].b; j = j + 1; } return s % 200; }`,
		56, 2, false},
	{"negative-field-literal",
		`struct N { v: i32, f: boolean } function neg(): N { return N { v: -7, f: true }; } function main(): i32 { let a: N = neg(); let t: i32 = 0; if (a.f) { t = t + 100; } return t + a.v; }`,
		93, 1, true},
	{"hex-field-literal",
		`struct N { v: i32, f: boolean } function hx(): N { return N { v: 0x1f, f: false }; } function main(): i32 { let a: N = hx(); let t: i32 = 0; if (a.f) { t = t + 100; } return t + a.v; }`,
		31, 1, true},
	{"reuse-shape-all-constant",
		`struct P { x: i32, y: i32 } function main(): i32 { let cond: i32 = 1; let r: i32 = 0; if (cond > 0) { let a: P = P { x: 10, y: 20 }; let s: i32 = a.x + a.y; let b: P = P { x: 3, y: 4 }; r = s + b.x + b.y; } return r; }`,
		37, 2, true},
	{"folded-field-admitted",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 0 - 3, b: 0x10 }; } function main(): i32 { let p: P = mk(); return (p.a + p.b + 100) % 200; }`,
		113, 1, false},
	{"wide-field-admitted",
		`struct W { n: i64, k: i32 } function mk(): W { return W { n: 5000000000, k: 3 }; } function main(): i32 { let w: W = mk(); if (w.n > 4000000000) { return w.k + 10; } return 0; }`,
		13, 1, true},
	{"f64-field-admitted",
		`struct F { x: f64, k: i32 } function mk(): F { return F { x: 2.5, k: 1 }; } function main(): i32 { let f: F = mk(); if (f.x > 2.0 && f.x < 3.0) { return f.k + 20; } return 0; }`,
		21, 1, true},
	{"nested-wide-records",
		`struct D { sec: i64, nsec: i64 } struct T { a: D, b: D } function mk(): T { return T { a: D { sec: 10 as i64, nsec: 0 }, b: D { sec: 30 as i64, nsec: 0 } }; } @noinline function keep(t: T): i64 { return t.a.sec + t.b.sec + t.a.nsec; } function main(): i32 { return keep(mk()) as i32; }`,
		40, 3, true},
}

func TestSelfHostConstAggregateWasmIR(t *testing.T) {
	boxedProbes(t)
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host const-aggregate wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range constAggWasmCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
			if tc.blocks > 0 {
				if heap, hok := watGlobalConst(wat, "$heap"); hok {
					if heap <= base {
						t.Errorf("%s: $heap = %d is not above the constant region base %d — freelist/heap bases did not follow the region", tc.name, heap, base)
					}
				}
			}
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
