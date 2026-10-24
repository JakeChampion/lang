package e2ecompiler

import (
	"strings"
	"testing"
)

const arrStructAppendChurnSrc = `struct Val { kind: i32, kids: i32[] }

function build(n: i32): i32 {
    let vals: Val[] = [];
    let i: i32 = 0;
    while (i < 4) { vals = vals.append(Val { kind: i, kids: [i, i + 1] }); i = i + 1; }
    return vals.len();
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + build(r); r = r + 1; }
    return t / 100;
}`

func TestSelfHostArrStructAppendReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, arrStructAppendChurnSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "arrstruct_append", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 4 {
		t.Fatalf("program exited %d, want 4", exit)
	}

	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatal("no leakcheck summary")
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatal("program allocated nothing — the probe is not exercising the path")
	}
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — an append-built array-of-structs must "+
			"reclaim its element field buffers, boxes and outer buffer, exactly as the "+
			"literal-built form of the same value does", summary, live)
	}
}

func TestSelfHostArrStructAppendHazardsX86_64(t *testing.T) {
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
			name: "element_alias",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    let vals: Val[] = [];
    vals = vals.append(Val { kind: 2, kids: [7, 8] });
    let q: Val = vals[0];
    return q.kids.len() + q.kind + vals.len();
}`,
			want: 5,
		},
		{
			name: "ident_element",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    let shared: Val = Val { kind: 5, kids: [1, 2, 3] };
    let vals: Val[] = [];
    vals = vals.append(shared);
    return vals[0].kind + shared.kids.len();
}`,
			want: 8,
		},
		{
			name: "payload_extract",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    let vals: Val[] = [];
    vals = vals.append(Val { kind: 1, kids: [4, 5] });
    let keep: i32[] = vals[0].kids;
    return keep.len() + keep[0] + vals.len();
}`,
			want: 7,
		},
		{
			name: "escaping_return",
			src: `struct Val { kind: i32, kids: i32[] }
function build(n: i32): Val[] {
    let vals: Val[] = [];
    vals = vals.append(Val { kind: n, kids: [n, n + 1] });
    return vals;
}
function main(): i32 { let r: Val[] = build(3); return r[0].kind + r[0].kids.len(); }`,
			want: 5,
		},
		{
			name: "rebound_to_other",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    let vals: Val[] = [];
    vals = vals.append(Val { kind: 4, kids: [4] });
    let qs: Val[] = [Val { kind: 9, kids: [9, 9] }];
    vals = qs;
    return vals[0].kind + qs[0].kids.len();
}`,
			want: 11,
		},
		{
			name: "base_copy_element",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    let base: Val = Val { kind: 2, kids: [6, 7] };
    let vals: Val[] = [];
    vals = vals.append(Val { ...base, kind: 3 });
    return vals[0].kind + base.kids.len();
}`,
			want: 5,
		},
		{
			name: "self_sourced_element",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    let vals: Val[] = [Val { kind: 1, kids: [3, 4] }];
    vals = vals.append(Val { kind: 2, kids: vals[0].kids });
    return vals[1].kids.len() + vals[0].kind;
}`,
			want: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "arrstruct_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"deep reclaim credit was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
