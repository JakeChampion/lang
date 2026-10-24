package e2ecompiler

import (
	"strings"
	"testing"
)

func borrowedParamAliasCancelCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			name: "read_through_alias",
			src: `function g(xs: i32[]): i32 {
    let v: i32[] = xs;
    return v[0] + v[1] + xs[2] + v.len();
}
function round(i: i32): i32 {
    let buf: i32[] = [i, i + 1, i + 2, i + 3];
    return g(buf);
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 87,
		},
		{
			name: "with_receiver_refused",
			src: `function g(xs: i32[]): i32 {
    let v: i32[] = xs;
    let w: i32[] = v.with(0, 99);
    return w[0] - v[0];
}
function round(i: i32): i32 {
    let buf: i32[] = [i, i + 1, i + 2, i + 3];
    let d: i32 = g(buf);
    if (d != 99 - i) { return 1000; }
    if (buf[0] != i) { return 1000; }
    return 1;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 6,
		},
		{
			name: "returned_alias_refused",
			src: `function g(xs: i32[]): i32[] {
    let v: i32[] = xs;
    return v;
}
function round(i: i32): i32 {
    let buf: i32[] = [i, i + 1, i + 2, i + 3];
    let out: i32[] = g(buf);
    return out[1] + buf[1];
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 42,
		},
	}
}

func TestSelfHostBorrowedParamAliasCancelX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range borrowedParamAliasCancelCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "bpaliascancel_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = read freed memory)", tc.name, exit, tc.want)
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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "bpaliascancel_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}
