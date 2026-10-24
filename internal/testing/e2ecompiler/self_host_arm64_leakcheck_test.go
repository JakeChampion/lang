package e2ecompiler

import (
	"strings"
	"testing"
)

type arm64LcCase struct {
	name    string
	src     string
	want    int
	verdict string
}

func arm64LcCases() []arm64LcCase {
	return []arm64LcCase{
		{
			name: "clean_string_churn",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); return t.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 21, verdict: "balanced",
		},
		{
			name: "leak_live_at_exit",
			src: `function bail(n: i32): i32 { exit(n); return 0; }
function main(): i32 { let keep: i32[][] = []; let i: i32 = 0; while (i < 3) { keep = keep.append([i, 2, 3, 4]); i = i + 1; } let r: i32 = bail(keep.len()); return r + keep.len(); }`,
			want: 3, verdict: "leaky",
		},
		{
			name: "heapfree_return",
			src:  `function main(): i32 { return 7; }`,
			want: 7, verdict: "zero",
		},
		{
			name: "heapfree_exit_builtin",
			src:  `function main(): i32 { exit(9); return 0; }`,
			want: 9, verdict: "zero",
		},
	}
}

func TestSelfHostArm64Leakcheck(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arm64LcCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCaptureEnv(t, x86runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}, "-target", "arm64-linux"))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "lc_"+tc.name, asm)
			cmd := runArm64Bin(qemu, bin)
			var errBuf strings.Builder
			cmd.Stderr = &errBuf
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Fatalf("%s exited %d, want %d — the census must not disturb the program", tc.name, code, tc.want)
			}
			summary := leakSummaryLine(errBuf.String())
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary on stderr (%q)", tc.name, errBuf.String())
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			switch tc.verdict {
			case "balanced":
				if strings.HasSuffix(summary, " from=exit") {
					t.Errorf("%s: %s — main returned, and the line says exit() printed it", tc.name, summary)
				}
				if allocs == 0 {
					t.Errorf("%s: allocs=0 — the probe exercised no allocation", tc.name)
				}
				if allocs != frees || live != 0 {
					t.Errorf("%s: %s — want a balanced census (allocs == frees, live 0)", tc.name, summary)
				}
			case "leaky":
				if allocs <= frees || live <= 0 {
					t.Errorf("%s: %s — this shape leaks soundly per round; a balanced census "+
						"here means the counters miss a free path or an alloc path", tc.name, summary)
				}
				if !strings.HasSuffix(summary, " from=exit") {
					t.Errorf("%s: %s — exit() printed this line, and it must say so", tc.name, summary)
				}
			case "zero":
				if allocs != 0 || frees != 0 || live != 0 {
					t.Errorf("%s: %s — a heap-free program must report zeros", tc.name, summary)
				}
			}
		})
	}
}

func TestSelfHostArm64LeakcheckOffEmitsNothing(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := arm64LcCases()[0].src
	off := string(runCaptureEnv(t, x86runner, driverBin, []byte(src),
		[]string{"PATH=/usr/bin:/bin"}, "-target", "arm64-linux"))
	for _, marker := range []string{"__fern_lc_", "leakcheck:"} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off asm contains %q — the census is not fully gated", marker)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "lc_off", off)
	cmd := runArm64Bin(qemu, bin)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 21 {
		t.Fatalf("flag-off run exited %d, want 21", code)
	}
	if errBuf.String() != "" {
		t.Errorf("flag-off run wrote stderr: %q", errBuf.String())
	}

	on := string(runCaptureEnv(t, x86runner, driverBin, []byte(src),
		[]string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}, "-target", "arm64-linux"))
	for _, marker := range []string{"__fern_lc_report", "__fern_lc_alloc_count", "leakcheck: allocs="} {
		if !strings.Contains(on, marker) {
			t.Errorf("flag-on asm is missing %q", marker)
		}
	}
}

func arm64CensusRun(t *testing.T, x86runner []string, driverBin, gcc, qemu, name, src string) int {
	t.Helper()
	asm := runCaptureEnv(t, x86runner, driverBin, []byte(src),
		[]string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}, "-target", "arm64-linux")
	return arm64Census(t, gcc, qemu, name, string(asm))
}

func arm64Census(t *testing.T, gcc, qemu, name, asm string) int {
	t.Helper()
	cmd := runArm64Bin(qemu, buildBinArm64(t, gcc, t.TempDir(), name, asm))
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(errBuf.String()), &allocs, &frees, &live); err != nil {
		t.Fatalf("%s: no leakcheck summary in %q: %v", name, errBuf.String(), err)
	}
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", name, allocs, frees, live)
	}
	return cmd.ProcessState.ExitCode()
}
