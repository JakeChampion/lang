package e2ecompiler

import (
	"testing"
)

type arrstructProdCase struct {
	name    string
	src     string
	want    int
	balance bool
}

const arrstructProdMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const arrstructProdDecl = "struct Val { kids: i32[], k: i32 }\n"

func arrstructProdCases() []arrstructProdCase {
	return []arrstructProdCase{
		{
			name: "producer_returns_local",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { let vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); vals = vals.append(Val { kids: [i + 2], k: i }); return vals; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 48, balance: true,
		},
		{
			name: "producer_returns_literal",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { return [Val { kids: [i, i + 1], k: i }, Val { kids: [i + 2], k: i }]; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 48, balance: true,
		},
		{
			name: "literal_init",
			src: arrstructProdDecl + `function round(i: i32): i32 { let v: Val[] = [Val { kids: [i, i + 1], k: i }, Val { kids: [i + 2], k: i }]; return v.len() + v[0].k; }` +
				arrstructProdMain,
			want: 48, balance: true,
		},
		{
			name: "sibling_alias",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { let vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); return vals; }
function round(base: Val[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: Val[] = mk(i);  t = t + v.len() + v[0].k; }
    if (i % 2 == 1) { let v: Val[] = base;   t = t + v.len() + v[0].k; }
    return t;
}
function main(): i32 { let b: Val[] = [Val { kids: [7, 8], k: 9 }]; let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 12,
		},
		{
			name: "producer_bare_ident_elem",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { let e: Val = Val { kids: [i, i + 1], k: i }; let vals: Val[] = []; vals = vals.append(e); return vals; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 14,
		},
		{
			name: "producer_foreign_rebind",
			src: arrstructProdDecl + `function other(i: i32): Val[] { return [Val { kids: [i], k: i }]; }
function mk(i: i32): Val[] { let vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); if (i % 3 == 0) { vals = other(i); } return vals; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 14,
		},
		{
			name: "producer_local_escapes",
			src: arrstructProdDecl + `function sink(vs: Val[]): i32 { return vs.len(); }
function mk(i: i32): Val[] { let vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); let n: i32 = sink(vals); return vals; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 14,
		},
	}
}

func TestSelfHostArrStructProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrstructProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrstructprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a share the "+
					"producer registry admitted without owning it)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0. Each element box and "+
					"its kids array are two thirds of the allocations, so a withheld "+
					"deep walk shows as frees at one third of allocs", tc.name, summary)
			}
		})
	}
}

func TestSelfHostArrStructProducerWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrstructProdCases() {
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
