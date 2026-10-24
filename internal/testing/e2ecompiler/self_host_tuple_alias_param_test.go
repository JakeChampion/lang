package e2ecompiler

import (
	"strings"
	"testing"
)

type tupleAliasParamCase struct {
	name string
	src  string
	want int
}

const tupleAliasParamMain = `
function main(): i32 {
    let keep: (i32, i32[]) = (5, [6, 7]);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func tupleAliasParamCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			name: "fnscope_alias_reads_only",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    let t: i32 = 0;
    let x: (i32, i32[]) = src;
    t = (t + x.0 + x.1.len()) % 101;
    return t;
}` + tupleAliasParamMain,
			want: 43,
		},
		{
			name: "if_block_alias_reads_only",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) {
        let x: (i32, i32[]) = src;
        t = (t + x.0 + x.1.len()) % 101;
        t = t + 1;
    }
    return t;
}` + tupleAliasParamMain,
			want: 75,
		},
		{
			name: "handout_elem_keeps_refused",
			src: `function get(src: (i32, i32[]), i: i32): i32[] {
    let x: (i32, i32[]) = src;
    return x.1;
}
function main(): i32 {
    let keep: (i32, i32[]) = (5, [6, 7]);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let out: i32[] = get(keep, i);
        let churn: i32[] = [i, i + 1, i + 2];
        acc = (acc + out.len() + churn.len()) % 101;
        i = i + 1;
    }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 20,
		},
		{
			name: "chained_alias_keeps_refused",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    let x: (i32, i32[]) = src;
    let y: (i32, i32[]) = x;
    return (y.0 + y.1.len()) % 101;
}` + tupleAliasParamMain,
			want: 43,
		},
		{
			name: "reassigned_alias_sound",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    let x: (i32, i32[]) = src;
    let t: i32 = (x.0 + x.1.len()) % 101;
    x = (i, [i, i + 1, i + 2]);
    t = (t + x.1.len()) % 101;
    return t;
}` + tupleAliasParamMain,
			want: 11,
		},
	}
}

func TestSelfHostTupleAliasParamX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleAliasParamCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupaliasparam_"+tc.name, asm)
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
			sanBin := buildBin(t, gcc, dir, "tupaliasparam_san_"+tc.name, sanAsm)
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
