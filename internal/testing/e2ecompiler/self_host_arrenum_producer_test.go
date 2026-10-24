package e2ecompiler

import (
	"testing"
)

type arrenumProdCase struct {
	name    string
	src     string
	want    int
	balance bool
}

const arrenumProdMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const arrenumProdDecl = "enum E { A(i32[]), B }\n"

func arrenumProdCases() []arrenumProdCase {
	return []arrenumProdCase{
		{
			name: "producer_returns_local",
			src: arrenumProdDecl + `function mk(i: i32): E[] { let xs: E[] = []; xs = xs.append(E.A([i, i + 1])); xs = xs.append(E.B); return xs; }
function round(i: i32): i32 { let v: E[] = mk(i); return v.len(); }` + arrenumProdMain,
			want: 68, balance: true,
		},
		{
			name: "producer_returns_literal",
			src: arrenumProdDecl + `function mk(i: i32): E[] { return [E.A([i, i + 1]), E.B]; }
function round(i: i32): i32 { let v: E[] = mk(i); return v.len(); }` + arrenumProdMain,
			want: 68, balance: true,
		},
		{
			name: "append_built_local",
			src: arrenumProdDecl + `function round(i: i32): i32 { let v: E[] = []; v = v.append(E.A([i, i + 1])); v = v.append(E.B); return v.len(); }` +
				arrenumProdMain,
			want: 68, balance: true,
		},
		{
			name: "literal_init",
			src: arrenumProdDecl + `function round(i: i32): i32 { let v: E[] = [E.A([i, i + 1]), E.B]; return v.len(); }` +
				arrenumProdMain,
			want: 68, balance: true,
		},
		{
			name: "append_bare_ident_elem",
			src: arrenumProdDecl + `function round(i: i32): i32 { let e: E = E.A([i, i + 1]); let v: E[] = []; v = v.append(e); return v.len(); }` +
				arrenumProdMain,
			want: 34,
		},
		{
			name: "append_then_extract",
			src: arrenumProdDecl + `function round(i: i32): i32 { let v: E[] = []; v = v.append(E.A([i, i + 1])); let e: E = v[0]; return v.len() + match (e) { E.A(p) => p.len(), E.B => 0 }; }` +
				arrenumProdMain,
			want: 19,
		},
		{
			name: "append_foreign_rebind",
			src: arrenumProdDecl + `function other(i: i32): E[] { return [E.A([i]), E.B]; }
function round(i: i32): i32 { let v: E[] = []; v = v.append(E.A([i, i + 1])); if (i % 3 == 0) { v = other(i); } return v.len(); }` + arrenumProdMain,
			want: 18,
		},
		{
			name: "producer_local_escapes",
			src: arrenumProdDecl + `function sink(vs: E[]): i32 { return vs.len(); }
function mk(i: i32): E[] { let xs: E[] = []; xs = xs.append(E.A([i, i + 1])); let n: i32 = sink(xs); return xs; }
function round(i: i32): i32 { let v: E[] = mk(i); return v.len(); }` + arrenumProdMain,
			want: 34,
		},
		{
			name: "sibling_alias",
			src: arrenumProdDecl + `function mk(i: i32): E[] { let xs: E[] = []; xs = xs.append(E.A([i, i + 1])); return xs; }
function round(base: E[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: E[] = mk(i);  t = t + v.len(); }
    if (i % 2 == 1) { let v: E[] = base;   t = t + v.len(); }
    return t;
}
function main(): i32 { let b: E[] = [E.A([7, 8])]; let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 17,
		},
	}
}

func TestSelfHostArrEnumProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrenumProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrenumprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the element walk "+
					"freed a box another owner still holds)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0. The element boxes and "+
					"their payload arrays are most of the allocations here, so a "+
					"withheld walk shows as frees at a fraction of allocs", tc.name, summary)
			}
		})
	}
}

func TestSelfHostArrEnumProducerWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrenumProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
