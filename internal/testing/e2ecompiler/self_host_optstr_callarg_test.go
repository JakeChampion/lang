package e2ecompiler

import (
	"strings"
	"testing"
)

func optstrCallargCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			name: "callarg_producer_payload",
			src: `function mk(a: string): string { return a + "!"; }
function peek(o: Option[string]): i32 {
    match (o) { Some(s) => { return s.len(); }, None => { return 0; } }
}
function round(i: i32): i32 {
    let o: Option[string] = Some(mk("abc"));
    return peek(o) + i % 3;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 14,
		},
		{
			name: "callarg_churn_read_back",
			src: `function mk(a: string): string { return a + "!"; }
function peek(o: Option[string]): i32 {
    match (o) { Some(s) => { return s.len(); }, None => { return 0; } }
}
function round(i: i32): i32 {
    let o: Option[string] = Some(mk("abcdefgh"));
    let churn: string = mk("zzzzzzzz");
    return peek(o) + churn.len() + i % 3;
}
function main(): i32 {
    let keep: string = mk("keepmeeee");
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    let ok: i32 = 0;
    if (keep == "keepmeeee!") { ok = 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (t + ok) % 97;
}`,
			want: 17,
		},
	}
}

func TestSelfHostOptStrCallargX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range optstrCallargCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optstrcallarg_"+tc.name, asm)
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
			sanBin := buildBin(t, gcc, dir, "optstrcallarg_san_"+tc.name, sanAsm)
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
