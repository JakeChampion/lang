package e2ecompiler

import (
	"strings"
	"testing"
)

var rcRuntimeCases = []struct {
	name string
	src  string
	exit int
}{
	{"rc-inc-dec", "function main(): i32 { let f: i32[] = [0]; let base: usize = __alloc(24); __store_i32(base + 8, 5); __fern_rc_inc(base + 16); __fern_rc_inc(base + 16); __fern_rc_dec(base + 16); return __load_i32(base + 8); }", 6},
	{"rc-underflow-detected", "function main(): i32 { let f: i32[] = [0]; let base: usize = __alloc(24); __store_i32(base + 8, 1); __fern_rc_dec(base + 16); __fern_rc_dec(base + 16); return __rc_underflow_count(); }", 1},
	{"rc-underflow-clean", "function main(): i32 { let f: i32[] = [0]; let base: usize = __alloc(24); __store_i32(base + 8, 3); __fern_rc_dec(base + 16); __fern_rc_dec(base + 16); return __rc_underflow_count(); }", 0},
	{"rc-inc-null-safe", "function main(): i32 { let f: i32[] = [0]; __fern_rc_inc(0); __fern_rc_dec(0); return 7; }", 7},
}

func TestSelfHostRcRuntimeX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range rcRuntimeCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostRcRuntimeArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range rcRuntimeCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

const (
	rcAliasUpdateSrc = "@noinline\nfunction mk(n: i32): i32[] { return [n, n + 1]; }\n@noinline\nfunction first(a: i32[]): i32 { return a[0]; }\n" +
		"function main(): i32 { let xs: i32[] = mk(1); let ys = xs; xs = xs.with(0, 5); return first(ys) + xs[0] + __rc_underflow_count(); }"
	rcReassignUpdateSrc = "@noinline\nfunction mk(n: i32): i32[] { return [n, n + 1]; }\n@noinline\nfunction first(a: i32[]): i32 { return a[0]; }\n" +
		"function main(): i32 { let xs: i32[] = mk(1); let ys: i32[] = mk(3); ys = xs; xs = xs.with(0, 5); return first(ys) + xs[0] + __rc_underflow_count(); }"
)

func TestSelfHostRcAliasIncX86_64(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"alias-read-both", "function main(): i32 { let xs: i32[] = [10, 20, 30]; let ys = xs; return ys[1] + xs[0]; }", 30},
		{"alias-chain", "function main(): i32 { let xs: i32[] = [4, 5, 6]; let ys = xs; let zs = ys; return zs[0] + ys[1] + xs[2]; }", 15},
		{"alias-no-underflow", "function main(): i32 { let xs: i32[] = [5, 6, 7]; let ys = xs; let zs = ys; return __rc_underflow_count(); }", 0},
		{"alias-struct-field", "struct H { items: i32[] } function main(): i32 { let h: H = H { items: [11, 22, 33] }; let y = h.items; return y[1] + h.items[2]; }", 55},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	t.Run("emits-retain-at-alias", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", rcAliasUpdateSrc)
		if rcIncSites(asm) == 0 {
			t.Errorf("expected a retain (__fern_rc_inc) at the live-source array alias; not found in emitted asm")
		}
		if code, _ := cli.runX86(t, asm); code != 6 {
			t.Errorf("exited %d, want 6 (11 = the retain did nothing: .with wrote through ys, and the sweep over-released)", code)
		}
		stderr, code := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "alias", cli.emit(t, "x86-64-linux", rcAliasUpdateSrc, "FERN_LEAKCHECK=1")))
		if code != 6 {
			t.Errorf("leakcheck build exited %d, want 6", code)
		}
		allocs, frees, live := parseLeakcheck(t, "emits-retain-at-alias", stderr)
		if allocs == 0 || allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d, want allocs == frees and live_bytes 0", allocs, frees, live)
		}
	})

	t.Run("elides-retain-at-move-alias", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", "function main(): i32 { let xs: i32[] = [1, 2]; let ys = xs; return ys[0]; }")
		if rcIncSites(asm) > 0 {
			t.Errorf("expected NO retain at the move-alias (the source's last mention transfers); found __fern_rc_inc in emitted asm")
		}
	})

	t.Run("emits-retain-at-field-alias", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", "struct H { items: i32[] } function main(): i32 { let h: H = H { items: [1, 2] }; let y = h.items; return y[0]; }")
		if rcIncSites(asm) == 0 {
			t.Errorf("expected a retain (__fern_rc_inc) at the struct-field array alias; not found in emitted asm")
		}
	})
}

func TestSelfHostRcReassignX86_64(t *testing.T) {
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"reassign-to-alias", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: i32[] = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }", 4},
		{"reassign-source-intact", "function main(): i32 { let xs: i32[] = [7, 8]; let ys: i32[] = [0, 0]; ys = xs; return xs[1] + ys[1]; }", 16},
		{"reassign-to-fresh", "function main(): i32 { let xs: i32[] = [1, 2]; xs = [9, 9, 9]; return xs[2]; }", 9},
		{"reassign-no-underflow", "function main(): i32 { let xs: i32[] = [1, 2]; let ys: i32[] = [3, 4]; ys = xs; let zs: i32[] = [5, 6]; zs = ys; return __rc_underflow_count(); }", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	t.Run("emits-retain-and-release", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", rcReassignUpdateSrc)
		if rcIncSites(asm) == 0 {
			t.Errorf("expected a retain (__fern_rc_inc) for the reassigned alias")
		}
		if code, _ := cli.runX86(t, asm); code != 6 {
			t.Errorf("exited %d, want 6 (11 = the retain did nothing: .with wrote through ys, and the sweep over-released)", code)
		}
		stderr, code := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "reassign", cli.emit(t, "x86-64-linux", rcReassignUpdateSrc, "FERN_LEAKCHECK=1")))
		if code != 6 {
			t.Errorf("leakcheck build exited %d, want 6", code)
		}
		allocs, frees, live := parseLeakcheck(t, "emits-retain-and-release", stderr)
		if allocs == 0 || allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d, want allocs == frees and live_bytes 0: the rebind must release ys's old buffer", allocs, frees, live)
		}
	})
}

func TestSelfHostRcExitSweepX86_64(t *testing.T) {
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"return-array", "function make(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2]; }", 4},
		{"borrowed-param", "function sum2(a: i32[]): i32 { return a[0] + a[1]; } function main(): i32 { let xs: i32[] = [7, 8]; let r = sum2(xs); return r + xs[0] + __rc_underflow_count(); }", 22},
		{"exit-sweep-no-underflow", "function f(): i32 { let xs: i32[] = [1, 2]; let ys = xs; return ys[0]; } function main(): i32 { let a = f(); let b = f(); let c = f(); return __rc_underflow_count(); }", 0},
		{"branch-local-unbound", "function main(): i32 { let xs: i32[] = [5, 6]; if (xs[0] > 100) { let ys: i32[] = [1, 2]; return ys[0]; } return xs[1] + __rc_underflow_count(); }", 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	for _, tc := range []struct {
		name, src, body string
		heap            bool
	}{
		{"original", "function main(): i32 { let xs: i32[] = [1, 2]; return xs[0]; }", "main", false},
		{"runtime", "@noinline function probe(n: i32): i32 { let xs: i32[] = [n, n + 1]; return xs[0]; } function main(): i32 { return probe(args().len()); }", "probe", true},
	} {
		t.Run("emits-exit-sweep/"+tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src)
			body := selfHostFnBody(t, []byte(asm), tc.body)
			for _, call := range []string{"call __fern_arr_box", "call __fn___fern_arr_dec"} {
				if strings.Contains(body, call) != tc.heap {
					t.Errorf("%s: expected %s presence=%v:\n%s", tc.name, call, tc.heap, body)
				}
			}
			if !tc.heap && !strings.Contains(body, ".K0(%rip)") {
				t.Errorf("original array has no static-data load:\n%s", body)
			}
			if code, out := cli.runX86(t, asm); code != 1 {
				t.Errorf("exit %d, want 1: %s", code, out)
			}
			if tc.heap {
				checked := cli.emit(t, "x86-64-linux", tc.src, "FERN_LEAKCHECK=1")
				stderr, code := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "sweep", checked))
				if code != 1 {
					t.Errorf("leakcheck exit %d, want 1", code)
				}
				assertBalancedCensus(t, stderr)
			}
		})
	}
}

func TestSelfHostRcMoveOnReturnX86_64(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"move-bare-local", "function make(): i32[] { let xs: i32[] = [10, 20, 30]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2] + __rc_underflow_count(); }", 40},
		{"move-chained", "function mk(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; } function relay(): i32[] { let a = mk(); return a; } function main(): i32 { let b = relay(); return b[0] + b[2] + __rc_underflow_count(); }", 4},
		{"move-with-sibling-sweep", "function make(): i32[] { let keep: i32[] = [9, 9]; let xs: i32[] = [5, 6, 7]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2] + __rc_underflow_count(); }", 12},
		{"return-param-not-moved", "@noinline function pick(a: i32[]): i32[] { return a; } function main(): i32 { let xs: i32[] = [3, 4]; let ys = pick(xs); return ys[0] + xs[1] + __rc_underflow_count(); }", 7},
		{"move-churn", "function build(n: i32): i32[] { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { let k = 0; let s = 0; while (k < 200000) { let r = build(64); s = r[63]; k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	t.Run("emits-no-inc-on-move", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux",
			"function make(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { let ys = make(); return ys[0]; }")
		if rcIncSites(asm) > 0 {
			t.Errorf("move-on-return should elide the retain inc, but found call __fn___fern_rc_inc")
		}
	})
	t.Run("emits-inc-when-not-moved", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux",
			"struct H { items: i32[] } function get(h: H): i32[] { return h.items; } function main(): i32 { let hh: H = H { items: [4, 5, 6] }; let ys = get(hh); return ys[0]; }")
		if rcIncSites(asm) == 0 {
			t.Errorf("returning a non-local array expression should still emit the retain inc")
		}
	})
}

func TestSelfHostRcArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"alias-read-both", "function main(): i32 { let xs: i32[] = [10, 20, 30]; let ys = xs; return ys[1] + xs[0]; }", 30},
		{"reassign-to-alias", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: i32[] = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }", 4},
		{"field-alias", "struct H { items: i32[] } function main(): i32 { let h: H = H { items: [11, 22, 33] }; let y = h.items; return y[1] + h.items[2]; }", 55},
		{"return-array", "function make(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2]; }", 4},
		{"borrowed-param", "function sum2(a: i32[]): i32 { return a[0] + a[1]; } function main(): i32 { let xs: i32[] = [7, 8]; let r = sum2(xs); return r + xs[0] + __rc_underflow_count(); }", 22},
		{"exit-sweep-no-underflow", "function f(): i32 { let xs: i32[] = [1, 2]; let ys = xs; return ys[0]; } function main(): i32 { let a = f(); let b = f(); let c = f(); return __rc_underflow_count(); }", 0},
		{"branch-local-unbound", "function main(): i32 { let xs: i32[] = [5, 6]; if (xs[0] > 100) { let ys: i32[] = [1, 2]; return ys[0]; } return xs[1] + __rc_underflow_count(); }", 6},
		{"self-append-no-underflow", "function main(): i32 { let xs: i32[] = []; let i = 0; while (i < 20) { xs = xs.append(i); i = i + 1; } return __rc_underflow_count(); }", 0},
		{"self-append-values", "function main(): i32 { let xs: i32[] = []; let i = 0; while (i < 20) { xs = xs.append(i * 2); i = i + 1; } return xs[19]; }", 38},
		{"struct-holds-array", "struct H { items: i32[] } function mk(): H { let xs: i32[] = [7, 8]; return H { items: xs }; } function main(): i32 { let h = mk(); return h.items[0] + h.items[1] + __rc_underflow_count(); }", 15},
		{"array-of-arrays", "@noinline function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let a: i32[] = id([1, 2]); let b: i32[] = id([3, 4]); let both: i32[][] = [a, b]; return both[0][1] + both[1][0] + __rc_underflow_count(); }", 5},
		{"struct-update-copy", "struct H { items: i32[], n: i32 } function main(): i32 { let xs: i32[] = [1, 2]; let h: H = H { items: xs, n: 0 }; let h2: H = H { ...h, n: 5 }; return h2.items[1] + h2.n + __rc_underflow_count(); }", 7},
		{"reclaim-churn", "function work(n: i32): i32 { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs[n - 1]; } function main(): i32 { let k = 0; let s = 0; while (k < 200000) { s = work(200); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 3},
		{"enum-holds-array", "enum Box { Arr(i32[]), Empty } function id(xs: i32[]): i32[] { return xs; } function mk(): Box { let xs: i32[] = id([3, 4, 5]); return Arr(xs); } function main(): i32 { let b = mk(); match (b) { Arr(a) => { return a[1] + a[2] + __rc_underflow_count(); }, Empty => { return 0; } } }", 9},
		{"move-bare-local", "function make(): i32[] { let xs: i32[] = [10, 20, 30]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2] + __rc_underflow_count(); }", 40},
		{"move-with-sibling-sweep", "function make(): i32[] { let keep: i32[] = [9, 9]; let xs: i32[] = [5, 6, 7]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2] + __rc_underflow_count(); }", 12},
		{"return-param-not-moved", "@noinline function pick(a: i32[]): i32[] { return a; } function main(): i32 { let xs: i32[] = [3, 4]; let ys = pick(xs); return ys[0] + xs[1] + __rc_underflow_count(); }", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostRcSelfMutateX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"self-append-no-underflow", "function main(): i32 { let xs: i32[] = []; let i = 0; while (i < 20) { xs = xs.append(i); i = i + 1; } return __rc_underflow_count(); }", 0},
		{"self-append-values", "function main(): i32 { let xs: i32[] = []; let i = 0; while (i < 20) { xs = xs.append(i * 2); i = i + 1; } return xs[19]; }", 38},
		{"reassign-different-clean", "function main(): i32 { let xs: i32[] = [1, 2]; let ys: i32[] = [3, 4]; ys = xs; return ys[0] + xs[1] + __rc_underflow_count(); }", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostRcConstructX86_64(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"struct-holds-array", "struct H { items: i32[] } function main(): i32 { let xs: i32[] = [1, 2, 3]; let h: H = H { items: xs }; return h.items[1] + xs[0] + __rc_underflow_count(); }", 3},
		{"struct-outlives-source", "struct H { items: i32[] } function mk(): H { let xs: i32[] = [7, 8]; return H { items: xs }; } function main(): i32 { let h = mk(); return h.items[0] + h.items[1] + __rc_underflow_count(); }", 15},
		{"struct-fresh-literal", "struct H { items: i32[] } function main(): i32 { let h: H = H { items: [4, 5, 6] }; return h.items[2] + __rc_underflow_count(); }", 6},
		{"struct-update-copy", "struct H { items: i32[], n: i32 } function main(): i32 { let xs: i32[] = [1, 2]; let h: H = H { items: xs, n: 0 }; let h2: H = H { ...h, n: 5 }; return h2.items[1] + h2.n + __rc_underflow_count(); }", 7},
		{"struct-update-override", "struct H { items: i32[], n: i32 } function main(): i32 { let xs: i32[] = [9, 8]; let h: H = H { items: [0], n: 1 }; let h2: H = H { ...h, items: xs }; return h2.items[0] + __rc_underflow_count(); }", 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
	t.Run("emits-retain-at-field-init", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", "struct H { items: i32[] } function main(): i32 { let xs: i32[] = [1, 2]; let h: H = H { items: xs }; return h.items[0] + xs[1]; }")
		if rcIncSites(asm) == 0 {
			t.Errorf("expected a retain (__fern_rc_inc) at the struct field init of an aliased local")
		}
	})

	t.Run("no-retain-when-the-field-init-moves", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", "struct H { items: i32[] }\n@noinline\nfunction first(h: H): i32 { return h.items[0]; }\n"+
			"function main(): i32 { let xs: i32[] = [1, 2]; let h: H = H { items: xs }; return first(h); }")
		if rcIncSites(asm) > 0 {
			t.Errorf("a moved local needs no retain at the field init — the box takes over its reference (#6726)")
		}
	})
}

func TestSelfHostRcConstructContainersX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"array-of-arrays", "@noinline function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let a: i32[] = id([1, 2]); let b: i32[] = id([3, 4]); let both: i32[][] = [a, b]; return both[0][1] + both[1][0] + __rc_underflow_count(); }", 5},
		{"tuple-of-array", "function main(): i32 { let xs: i32[] = [7, 8]; let t = (xs, 9); return t.0[1] + t.1 + __rc_underflow_count(); }", 17},
		{"return-arr-of-arrs", "@noinline function id(xs: i32[]): i32[] { return xs; } function mk(): i32[][] { let a: i32[] = id([5, 6]); return [a, a]; } function main(): i32 { let both = mk(); return both[0][0] + both[1][1] + __rc_underflow_count(); }", 11},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostRcClosureX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"closure-captures-array", "function main(): i32 { let xs: i32[] = [3, 4, 5]; let f = (): i32 => { return xs[1] + xs[2]; }; return f() + __rc_underflow_count(); }", 9},
		{"closure-escapes-with-array", "function mk(xs: i32[]): () => i32 { return (): i32 => { return xs[0] + xs[1]; }; } function main(): i32 { let a: i32[] = [3, 4]; let f = mk(a); return f() + __rc_underflow_count(); }", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostRcFreeReclaimX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"reclaim-churn", "function work(n: i32): i32 { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs[n - 1]; } function main(): i32 { let k = 0; let s = 0; while (k < 500000) { s = work(200); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 3},
		{"borrowed-param-builder", "function add(toks: i32[], t: i32): i32[] { return toks.append(t); } function main(): i32 { let ts: i32[] = []; let i = 0; while (i < 200) { ts = add(ts, i); i = i + 1; } return ts[199] + __rc_underflow_count(); }", 199},
		{"enum-holds-array", "enum Box { Arr(i32[]), Empty } function id(xs: i32[]): i32[] { return xs; } function mk(): Box { let xs: i32[] = id([3, 4, 5]); return Arr(xs); } function main(): i32 { let b = mk(); match (b) { Arr(a) => { return a[1] + a[2] + __rc_underflow_count(); }, Empty => { return 0; } } }", 9},
		{"loop-local-rebind", "function build(n: i32): i32[] { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { let s = 0; let k = 0; while (k < 100000) { let r: i32[] = build(32); s = s + r[31]; k = k + 1; } return (s % 5) + __rc_underflow_count(); }", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostRcStructArrayFieldDropX86_64(t *testing.T) {
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"struct-arr-field-fresh-no-underflow", "struct E { v: i32 } struct H { es: E[] } function step(n: i32): i32 { let h = H { es: [E { v: n }, E { v: n + 1 }] }; return h.es[0].v; } function main(): i32 { let s = 0; let i = 0; while (i < 2000) { s = s + step(i); i = i + 1; } return s - s + __rc_underflow_count(); }", 0},
		{"struct-arr-field-alias-no-underflow", "struct E { v: i32 } struct H { es: E[] } function id(xs: i32[]): i32[] { return xs; } function wrapH(src: E[]): i32 { let h = H { es: src }; return h.es[0].v; } function main(): i32 { let shared: E[] = [E { v: id([3])[0] }, E { v: 4 }]; let s = 0; let i = 0; while (i < 2000) { s = s + wrapH(shared); i = i + 1; } return s - s + __rc_underflow_count(); }", 0},
		{"struct-arr-field-callvalue-no-underflow", "struct E { v: i32 } struct H { es: E[] } function mk(n: i32): E[] { return [E { v: n }, E { v: n * 2 }]; } function step(n: i32): i32 { let h = H { es: mk(n) }; return h.es[1].v; } function main(): i32 { let s = 0; let i = 0; while (i < 2000) { s = s + step(i); i = i + 1; } return s - s + __rc_underflow_count(); }", 0},
		{"enum-arr-field-no-underflow", "enum K { A(i32), B } struct G { ks: K[] } function step(n: i32): i32 { let g = G { ks: [A(n), B] }; return match (g.ks[0]) { A(x) => x, B => 0 }; } function main(): i32 { let s = 0; let i = 0; while (i < 2000) { s = s + step(i); i = i + 1; } return s - s + __rc_underflow_count(); }", 0},
		{"struct-arr-field-value", "struct E { v: i32 } struct H { es: E[] } function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let h = H { es: [E { v: id([5])[0] }, E { v: 9 }] }; return h.es[0].v * 10 + h.es[1].v; }", 59},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	t.Run("emits-struct-array-field-drop", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux",
			"struct E { v: i32 } struct H { es: E[] } function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let h = H { es: [E { v: id([1])[0] }] }; h = H { es: [E { v: id([2])[0] }] }; return h.es[0].v; }")
		body := asmFuncBody(t, asm, "__fn___sem_drop_H")
		if n := strings.Count(body, "call __fn___fern_arr_dec"); n < 2 {
			t.Errorf("expected H's drop to release each element and then the buffer (__fern_arr_dec at least twice), got %d:\n%s", n, body)
		}
		if rcIsUniqueSites(body) == 0 {
			t.Errorf("expected the element-walk sole-owner gate in H's drop; not found:\n%s", body)
		}
	})
}
