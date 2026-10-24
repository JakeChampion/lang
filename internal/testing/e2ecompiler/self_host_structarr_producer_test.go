package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type structarrProdCase struct {
	name    string
	src     string
	want    int
	balance bool
}

const structarrProdDecl = "struct P { s: string, n: i32 }\n" +
	"function w(a: string): string { return a + \"!\"; }\n"

const structarrProdMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func structarrProdCases() []structarrProdCase {
	return []structarrProdCase{
		{
			name: "producer_returns_local",
			src: structarrProdDecl + `function mk(): P[] { let a: P[] = [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; return a; }
function round(i: i32): i32 { let v: P[] = mk(); return v.len(); }` + structarrProdMain,
			want: 68, balance: true,
		},
		{
			name: "producer_returns_literal",
			src: structarrProdDecl + `function mk(): P[] { return [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; }
function round(i: i32): i32 { let v: P[] = mk(); return v.len(); }` + structarrProdMain,
			want: 68, balance: true,
		},
		{
			name: "literal_init",
			src: structarrProdDecl + `function round(i: i32): i32 { let v: P[] = [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; return v.len(); }` +
				structarrProdMain,
			want: 68, balance: true,
		},
		{
			name: "producer_append_built",
			src: structarrProdDecl + `function mk(i: i32): P[] { let a: P[] = []; a = a.append(P { s: w("p"), n: i }); a = a.append(P { s: w("q"), n: i }); return a; }
function round(i: i32): i32 { let v: P[] = mk(i); return v.len() + v[0].n; }` + structarrProdMain,
			want: 48, balance: true,
		},
		{
			name: "readback_beside_fresh",
			src: structarrProdDecl + `function mk(): P[] { let a: P[] = [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; return a; }
function round(i: i32): i32 {
    let v: P[] = mk();
    let junk: P[] = [P { s: w("zz"), n: 9 }, P { s: w("yy"), n: 8 }];
    return v[0].s.len() + v[1].n + junk[0].n;
}` + structarrProdMain,
			want: 27, balance: true,
		},
		{
			name: "sibling_alias",
			src: structarrProdDecl + `function mk(i: i32): P[] { let a: P[] = [P { s: w("p"), n: i }]; return a; }
function round(base: P[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: P[] = mk(i); t = t + v.len() + v[0].n; }
    if (i % 2 == 1) { let v: P[] = base;  t = t + v.len() + v[0].n; }
    return t;
}
function main(): i32 {
    let b: P[] = [P { s: w("base"), n: 7 }];
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 78, balance: true,
		},
		{
			name: "producer_bare_ident_elem",
			src: structarrProdDecl + `function mk(i: i32): P[] { let e: P = P { s: w("p"), n: i }; let a: P[] = []; a = a.append(e); return a; }
function round(i: i32): i32 { let v: P[] = mk(i); return v.len() + v[0].n; }` + structarrProdMain,
			want: 14,
		},
		{
			name: "producer_returns_param",
			src: structarrProdDecl + `@noinline function passthru(a: P[]): P[] { return a; }
function round(i: i32): i32 {
    let src: P[] = [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }];
    let v: P[] = passthru(src);
    return v.len() + src.len();
}` + structarrProdMain,
			want: 53,
		},
		{
			name: "producer_param_string_field",
			src: structarrProdDecl + `function mkp(nm: string): P[] { return [P { s: nm, n: 1 }]; }
function round(i: i32): i32 {
    let owned: string = w("keepvalue");
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < 4) {
        let v: P[] = mkp(owned);
        acc = acc + v.len() + v[0].s.len();
        j = j + 1;
    }
    let junk1: string = w("ZZZZZZZZZZ");
    let junk2: string = w("YYYYYYYYYY");
    return acc + owned.len() + junk1.len() + junk2.len();
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 47,
		},
		{
			name: "arrfield_elem_stays_arrstruct",
			src: `struct Q { ys: i32[], n: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
function mkq(): Q[] { let a: Q[] = [Q { ys: id([1, 2]), n: 1 }, Q { ys: id([3]), n: 2 }]; return a; }
function round(i: i32): i32 { let v: Q[] = mkq(); return v.len() + v[0].ys.len(); }` + structarrProdMain,
			want: 53, balance: true,
		},
	}
}

func TestSelfHostStructArrProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structarrProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "structarrprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a reference the "+
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
				t.Errorf("%s: %s — must balance at live_bytes 0. The two element boxes "+
					"and the outer buffer are what an uncredited binding strands, so a "+
					"withheld element walk shows as frees at one seventh of allocs",
					tc.name, summary)
			}
		})
	}
}

func TestSelfHostStructArrProducerWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping structarr producer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range structarrProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "structarrprod_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s: wasm exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostStructArrProducerIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structarrProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "structarrprod_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s: arm64 exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
