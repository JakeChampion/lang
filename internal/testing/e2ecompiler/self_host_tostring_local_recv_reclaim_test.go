package e2ecompiler

import (
	"fmt"
	"testing"
)

func toStringLocalSrc(helper string, rounds int) string {
	return fmt.Sprintf(`import "std/i32";
%s
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let s: string = fmt(i); acc = (acc + s.len()) %% 91; i = i + 1; }
    return acc;
}
function main(): i32 { return churn(%d) + __rc_underflow_count() * 100; }
`, helper, rounds)
}

var toStringLocalFlatCases = []struct {
	name    string
	helper  string
	want100 int
	want200 int
}{
	{
		name: "local_recv",
		helper: `function fmt(n: i32): string {
    let v: i32 = n * 2;
    return v.to_string();
}`,
		want100: 63,
		want200: 90,
	},
	{
		name: "local_recv_via_binding",
		helper: `function fmt(n: i32): string {
    let v: i32 = n * 2;
    let r: string = v.to_string();
    return r;
}`,
		want100: 63,
		want200: 90,
	},
	{
		name: "i64_local_recv",
		helper: `function fmt(n: i32): string {
    let v: i64 = (n as i64) * 2;
    return v.to_string();
}`,
		want100: 63,
		want200: 90,
	},
	{
		name: "nested_block_decls",
		helper: `function fmt(n: i32): string {
    if (n % 2 == 0) { let v: i32 = n * 2; return v.to_string(); }
    let w: i32 = n * 3;
    return w.to_string();
}`,
		want100: 71,
		want200: 7,
	},
}

var toStringLocalHazardCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "shadowed_by_nonscalar",
		src: toStringLocalSrc(`function fmt(n: i32): string {
    let v: i32 = n * 2;
    if (n > 1000000) { let v: string = "x"; return v; }
    return v.to_string();
}`, 200),
		want: 90,
	},
	{
		name: "unannotated_local",
		src: toStringLocalSrc(`function fmt(n: i32): string {
    let v = n * 2;
    return v.to_string();
}`, 200),
		want: 90,
	},
	{
		name: "struct_recv_method",
		src: toStringLocalSrc(`struct P { a: i32 }
function (p: P) to_string(): string { return "p"; }
function fmt(n: i32): string {
    let v: P = P { a: n };
    return v.to_string();
}`, 200),
		want: 18,
	},
}

func TestSelfHostToStringLocalRecvReclaimX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range toStringLocalFlatCases {
		t.Run(tc.name, func(t *testing.T) {
			live := func(rounds, want int) int64 {
				t.Helper()
				src := toStringLocalSrc(tc.helper, rounds)
				asm := cli.emit(t, "x86-64-linux", src, "FERN_LEAKCHECK=1")
				name := fmt.Sprintf("tostrlocal_%s_%d", tc.name, rounds)
				stderr, exit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), name, asm))
				if exit != want {
					t.Fatalf("%s exited %d, want %d (a value >= 100 is an rc over-release, not a leak)",
						name, exit, want)
				}
				summary := leakSummaryLine(stderr)
				if summary == "" {
					t.Fatalf("%s: no leakcheck summary in %q", name, stderr)
				}
				var allocs, frees, l int64
				if _, err := fmtSscan(summary, &allocs, &frees, &l); err != nil {
					t.Fatalf("%s: parse %q: %v", name, summary, err)
				}
				if allocs == 0 {
					t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
				}
				t.Logf("%s: %s", name, summary)
				return l
			}
			l100, l200 := live(100, tc.want100), live(200, tc.want200)
			if l100 != l200 {
				t.Errorf("a `.to_string()` on a callee LOCAL leaks per round: live_bytes=%d at 100 "+
					"rounds and %d at 200. The same helper with a PARAM receiver is flat, so the "+
					"registry proof is what is missing, not the lowering (#7193 follow-up)", l100, l200)
			}
		})
	}
}

func TestSelfHostToStringLocalRecvHazardsX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range toStringLocalHazardCases {
		t.Run(tc.name, func(t *testing.T) {
			if exit, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); exit != tc.want {
				t.Errorf("exited %d, want %d — %d+100 would be an rc over-release", exit, tc.want, tc.want)
			}
		})
	}
}

func toStringLocalAllCases() []struct {
	name string
	src  string
	want int
} {
	var out []struct {
		name string
		src  string
		want int
	}
	for _, tc := range toStringLocalFlatCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, toStringLocalSrc(tc.helper, 200), tc.want200})
	}
	for _, tc := range toStringLocalHazardCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, tc.src, tc.want})
	}
	return out
}

func TestSelfHostToStringLocalRecvArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range toStringLocalAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}

func TestSelfHostToStringLocalRecvWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range toStringLocalAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}
