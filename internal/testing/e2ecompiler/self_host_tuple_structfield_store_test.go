package e2ecompiler

import (
	"strings"
	"testing"
)

func tupleStructFieldStoreCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			name: "local_store_balances",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    let k: (i32, i32[]) = (i, [i, i + 1]);
    let h: Hold = Hold { t: k, n: i };
    return h.n + k.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 23,
		},
		{
			name: "two_holders_balances",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    let k: (i32, i32[]) = (i, [i, i + 1]);
    let h1: Hold = Hold { t: k, n: i };
    let h2: Hold = Hold { t: k, n: i + 1 };
    return h1.n + h2.n + k.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 10,
		},
		{
			name: "read_through_both_owners",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    let k: (i32, i32[]) = (i, [i, i + 1]);
    let h: Hold = Hold { t: k, n: i };
    let a: i32 = h.t.1.len();
    let b: i32 = k.1.len();
    return a + b + h.n;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 38,
		},
		{
			name: "fresh_literal_field_balances",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    let h: Hold = Hold { t: (i, [i, i + 1]), n: i };
    return h.n + h.t.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 23,
		},
		{
			name: "returned_holder_balances",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function mk(i: i32): Hold {
    let k: (i32, i32[]) = (i, [i, i + 1]);
    return Hold { t: k, n: i };
}
function round(i: i32): i32 {
    let h: Hold = mk(i);
    return h.n + h.t.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 23,
		},
		{
			name: "array_of_holders_stays_refused",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    let k: (i32, i32[]) = (i, [i, i + 1]);
    let hs: Hold[] = [Hold { t: k, n: i }, Hold { t: k, n: i + 1 }];
    return hs.len() + hs[0].n;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 4,
		},
	}
}

func TestSelfHostTupleStructFieldStoreX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleStructFieldStoreCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupsfs_"+tc.name, asm)
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
			sanBin := buildBin(t, gcc, dir, "tupsfs_san_"+tc.name, sanAsm)
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
