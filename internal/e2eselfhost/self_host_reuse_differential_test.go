package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStrarrReuseExclusionX86_64 runs the string[] AND fn reuse
// shapes whose value is ALIASED (a bare local ident as a donor field / a
// self-overwrite override): the alias stays usable after the second
// construction (values cross-checked against native -interp: 10 / 4 / 25 / 18
// / 8 / 12), with the rc-underflow detector clean.
func TestSelfHostStrarrReuseExclusionX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"aliased-donor-field", `struct P { tags: string[], n: i32 } function main(): i32 { var xs: string[] = ["k", "m"]; var a: P = P { tags: xs, n: 1 }; var s1: i32 = a.tags.len() + a.n; var b: P = P { tags: ["z"], n: 2 }; var live: i32 = xs.len() + xs[0].len() + xs[1].len(); if (__rc_underflow_count() != 0) { return 99; } return s1 + b.tags.len() + b.n + live; }`, 10},
		{"aliased-override", `struct P { tags: string[], n: i32 } function main(): i32 { var xs: string[] = ["k"]; var d: P = P { tags: ["x"], n: 1 }; var c: P = P { ...d, tags: xs, n: 2 }; var live: i32 = xs[0].len(); if (__rc_underflow_count() != 0) { return 99; } return c.tags.len() + c.n + live; }`, 4},
		{"aliased-fn-donor-field", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var g = (x: i32): i32 => { return x * 2; }; var a: H = H { f: g, id: 1 }; var s1: i32 = a.f(5) + a.id; var b: H = H { f: (x: i32): i32 => { return x + 1; }, id: 2 }; var live: i32 = g(3); if (__rc_underflow_count() != 0) { return 99; } return s1 + b.f(5) + b.id + live; }`, 25},
		{"aliased-fn-override", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var g = (x: i32): i32 => { return x * 2; }; var d: H = H { f: (x: i32): i32 => { return x + 1; }, id: 1 }; var c: H = H { ...d, f: g, id: 2 }; var live: i32 = g(3); if (__rc_underflow_count() != 0) { return 99; } return c.f(5) + c.id + live; }`, 18},
		{"aliased-boxarr-donor-field", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { var xs: In[] = [In { k: 1, n: 2 }]; var a: W = W { items: xs, id: 1 }; var s1: i32 = a.items.len() + a.id; var b: W = W { items: [In { k: 5, n: 6 }], id: 2 }; var live: i32 = xs[0].k + xs[0].n; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.items.len() + b.id + live; }`, 8},
		// #5342 own-param string / enum admissions: a bare local as the string
		// override is an uncounted alias (cross_recipient_fields_fresh refuses),
		// and a string-fielded type that does not ROUTE field reclaim (`get`
		// returns `x.s`, which the routing scan reads as an unsafe read of every
		// `s` field) took no retain at the caller's construction, so no family
		// may free its old value.
		{"aliased-string-own-override", `struct P { s: string, n: i32 } function f(own d: P): i32 { var t: string = "aliased-local-payload"; var c = P { ...d, s: t }; return c.n + c.s.len() + t.len(); } function main(): i32 { if (f(P { s: "abcdefghij-longer", n: 3 }) != 45) { return 98; } return __rc_underflow_count(); }`, 0},
		{"unrouted-string-own-donor", `struct P { s: string, n: i32 } function get(x: P): string { return x.s; } function f(own d: P): i32 { var u: i32 = d.n; var c = P { ...d, s: "override-literal-payload" }; return c.n + c.s.len() + u; } function main(): i32 { var h: P = P { s: "abcdefghij-longer", n: 3 }; var r: i32 = f(P { s: h.s, n: 4 }); var g: string = get(h); if (r + g.len() != 49) { return 98; } return __rc_underflow_count(); }`, 0},
		{"rcfield-element-type-excluded", `struct In2 { xs: i32[], k: i32 } struct W { items: In2[], id: i32 } function main(): i32 { var a: W = W { items: [In2 { xs: [1, 2], k: 3 }], id: 1 }; var s1: i32 = a.items.len() + a.items[0].k + a.id; var b: W = W { items: [In2 { xs: [4], k: 5 }], id: 2 }; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.items.len() + b.items[0].xs[0] + b.id; }`, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d (99 = over-release)", tc.name, code, tc.want)
			}
		})
	}
}
