package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const tupStrElemW = "function w(a: string): string { return a + \"!\"; }\n"

const tupStrElemMain = "\nfunction main(): i32 { let x: i32 = 0; let r: i32 = 0; " +
	"while (r < 100) { x = x + round(r); r = r + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return x % 83; }"

type tupStrElemCase struct {
	name string
	src  string
	want int
}

func tupStrElemCases() []tupStrElemCase {
	return []tupStrElemCase{
		{
			name: "str_elem",
			src: tupStrElemW + `function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    return t.0 + t.1.len();
}` + tupStrElemMain,
			want: 21,
		},
		{
			name: "str_read_after",
			src: tupStrElemW + `function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    return t.1.len() + s.len() + i;
}` + tupStrElemMain,
			want: 72,
		},
		{
			name: "two_str_elems",
			src: tupStrElemW + `function round(i: i32): i32 {
    let a: string = w("ab");
    let b: string = w("cd");
    let t: (string, string) = (a, b);
    return t.0.len() + t.1.len() + i;
}` + tupStrElemMain,
			want: 72,
		},
		{
			name: "mixed_str_and_array",
			src: tupStrElemW + `function round(i: i32): i32 {
    let s: string = w("ab");
    let xs: i32[] = [i, i + 1];
    let t: (string, i32[]) = (s, xs);
    return t.0.len() + t.1[0] + i;
}` + tupStrElemMain,
			want: 74,
		},
		{
			name: "litstr_elem",
			src: tupStrElemW + `function round(i: i32): i32 {
    let s: string = "abcd";
    let t: (i32, string) = (i, s);
    return t.1.len() + i;
}` + tupStrElemMain,
			want: 38,
		},
		{
			name: "last_use_before_return",
			src: tupStrElemW + `function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    let acc: i32 = t.1.len();
    return acc + i;
}` + tupStrElemMain,
			want: 21,
		},
		{
			name: "cross_reuse_donor",
			src: tupStrElemW + `function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    let a: i32 = t.1.len();
    let u: string = w("cde");
    let v: (i32, string) = (i, u);
    return a + v.1.len() + i;
}` + tupStrElemMain,
			want: 6,
		},
	}
}

func TestSelfHostTupleStrElemRetainX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupStrElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupstrelem_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: __fern_str_free "+
					"claimed a box someone else still owns)", tc.name, exit, tc.want)
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
				t.Fatalf("%s allocated nothing — the probe is not exercising the path. "+
					"A string built by constant folding is an immortal literal (rc = -1) "+
					"and measures nothing; every probe here goes through w()", tc.name)
			}
			if live != 0 {
				t.Errorf("%s: %s — live_bytes must be 0. The retain is per round, so "+
					"anything stranded here scales with the loop", tc.name, summary)
			}
			if allocs != frees {
				t.Errorf("%s: %s — allocs and frees must balance exactly; frees above "+
					"allocs is a double free, not an improvement", tc.name, summary)
			}
		})
	}
}

func TestSelfHostTupleStrElemHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			name: "str_elem_extracted_local",
			src: tupStrElemW + `function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    let u: string = t.1;
    return u.len() + i;
}` + tupStrElemMain,
			want: 21,
		},
		{
			name: "str_elem_extracted_escaping",
			src: tupStrElemW + `function grab(i: i32): string {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    return t.1;
}
function round(i: i32): i32 { let g: string = grab(i); return g.len() + i; }` + tupStrElemMain,
			want: 21,
		},
		{
			name: "ident_used_twice_in_tuple",
			src: tupStrElemW + `function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (string, i32) = (s, s.len());
    return t.0.len() + t.1 + i;
}` + tupStrElemMain,
			want: 72,
		},
		{
			name: "untaken_branch_null",
			src: tupStrElemW + `function round(i: i32): i32 {
    let acc: i32 = 0;
    if (i % 2 == 0) { let s: string = w("ab"); let t: (i32, string) = (i, s); acc = t.1.len(); }
    return acc + i;
}` + tupStrElemMain,
			want: 37,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupstrhaz_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the element release "+
					"claimed a reference the frame handed to another owner)", tc.name, exit, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", tc.name, allocs, frees, live)
			}
		})
	}
}

func TestSelfHostTupleStrElemRetainWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple string-element retain wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupStrElemCases() {
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
			watFile := filepath.Join(dir, "tupstrelem_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple string-element retain wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostTupleStrElemRetainIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupStrElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupstrelem_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
