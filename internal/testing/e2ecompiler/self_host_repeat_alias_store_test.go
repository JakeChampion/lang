package e2ecompiler

import (
	"strings"
	"testing"
)

const repeatAliasSrc = `function round(n: i32): i32 {
    let a: i32[] = [n, n, n, n, n];
    let b: i32[] = [7];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 3) { b = a; t = t + b[0]; i = i + 1; }
    return t;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 3;
}`

const repeatAliasRowSrc = `function round(n: i32): i32 {
    let g: i32[][] = [];
    let held: i32[] = [7];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        g = g.append([n, n]);
        held = g[0];
        t = t + held[0] + g[i][1];
        i = i + 1;
    }
    return t + held[1];
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 3;
}`

const repeatSelfMutationSrc = `function round(n: i32): i32 {
    let xs: i32[] = [n];
    let i: i32 = 0;
    while (i < 6) { xs = xs.append(n + i); i = i + 1; }
    return xs[0] + xs[3] + xs.len();
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 7;
}`

func TestSelfHostRepeatAliasStoreX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"repeated_alias", repeatAliasSrc},
		{"repeated_row_alias", repeatAliasRowSrc},
		{"self_mutation_keeps_guard", repeatSelfMutationSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, nativeExit := nativeLeakVerdict(t, cli, dir, "repalias_nat_"+tc.name, tc.src)
			if nativeExit < 0 {
				t.Fatalf("native side did not run (exit %d)", nativeExit)
			}

			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "repalias_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != nativeExit {
				t.Fatalf("self-host exited %d, native %d — a mismatched release on an "+
					"overwrite frees a buffer the program still holds", exit, nativeExit)
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
				t.Errorf("%s: live_bytes=%d (allocs=%d frees=%d), want 0 — an overwrite "+
					"that retained the new reference owes a release of the old one even "+
					"when they are the same pointer", summary, live, allocs, frees)
			}
		})
	}
}
