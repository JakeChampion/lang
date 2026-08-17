package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The closure-call census (#6638 defunctionalise S0). `irlower_run -clocensus`
// counts the env-first closure dispatches in a module's lowered IR and splits
// them by where the env box comes from, because that is what decides how much
// analysis a devirtualising rewrite would need:
//
//	env_local  built here by a flat `const_func … arr_make`, single-assigned
//	env_call   returned by an adjacent `call_direct`
//	env_param  arrives as a parameter — the higher-order case
//	env_other  multiply-assigned, or any other provenance
//	plain      a fn-POINTER indirect call, carrying no env box at all
//
// These tests exist because the number decided a roadmap row: over the
// conformance corpus, the stdlib, and the self-hosted compiler's own modules
// the whole population is 91 sites in 535k lowered ops, 73 of them env_param.
// A measurement that steers a decision has to be reproducible and has to fail
// when the thing it measured moves, or the decision silently outlives its
// evidence.

// censusLine runs the census over one source and returns its single line.
func censusLine(t *testing.T, bin, src string) string {
	t.Helper()
	cmd := exec.Command(bin, "-clocensus")
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("census driver failed: %v", err)
	}
	line := strings.TrimSpace(string(out))
	if !strings.HasPrefix(line, "clocensus: ") {
		t.Fatalf("census output is not a census line: %q", line)
	}
	return line
}

// censusField reads one count out of a census line.
func censusField(t *testing.T, line, key string) int {
	t.Helper()
	for _, tok := range strings.Fields(line)[1:] {
		k, v, ok := strings.Cut(tok, "=")
		if !ok || k != key {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
			t.Fatalf("census field %s=%q is not a number in %q", key, v, line)
		}
		return n
	}
	t.Fatalf("census line has no %s field: %q", key, line)
	return 0
}

// TestSelfHostClosureCallCensusBuckets pins one program per provenance bucket.
//
// The load-bearing case is `called-only-lambda`, which counts ZERO: a lambda
// that is only ever called never reaches an indirect dispatch at all, because
// try_lift_binding direct-calls it. That is why env_local is ~0 across the whole
// measurement, and it is the single fact the "do not build defunctionalise"
// conclusion rests on — so it is pinned as a case rather than left as prose. The
// bucket only fills when the same lambda ALSO escapes (`escaping-and-called`),
// which is the residual population a local rewrite could actually win.
func TestSelfHostClosureCallCensusBuckets(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	cases := []struct {
		name string
		src  string
		// want is the closure half of the census line: everything from `env=`
		// on. The `ops=` tally is deliberately not pinned — it moves with any
		// lowering change and carries no closure signal.
		want string
	}{
		{
			name: "no-closures",
			src:  `function main(): i32 { var a: i32 = 1; return a + 2; }`,
			want: "env=0 env_local=0 env_call=0 env_param=0 env_other=0 plain=0",
		},
		{
			// A fn-POINTER array. No env box: the element IS the code address,
			// so the call is `load … ; call_indirect(argc)` with no box read.
			// A different rewrite from every env-first case below.
			name: "fn-pointer-array",
			src: `function a(x: i32): i32 { return x + 1; }
function b(x: i32): i32 { return x + 2; }
function main(): i32 {
    var fs: ((i32) => i32)[] = [a, b];
    return fs[0](1) + fs[1](2);
}`,
			want: "env=0 env_local=0 env_call=0 env_param=0 env_other=0 plain=2",
		},
		{
			// A fn-typed PARAMETER — env-first even for a non-capturing callee,
			// because the lift boxes every fn value handed across a call. This
			// is the bucket the stdlib combinators live in, and it is 80% of the
			// whole population.
			name: "fn-typed-param",
			src: `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function inc(x: i32): i32 { return x + 1; }
function main(): i32 { return apply(inc, 41); }`,
			want: "env=1 env_local=0 env_call=0 env_param=1 env_other=0 plain=0",
		},
		{
			// A closure RETURNED by a callee: the box is single-assigned from an
			// adjacent call_direct, so resolving it means reading that one
			// function's returns.
			name: "returned-closure",
			src: `function mk(n: i32): (i32) => i32 { return function(x: i32): i32 { return x + n; }; }
function main(): i32 { var f = mk(5); return f(37); }`,
			want: "env=1 env_local=0 env_call=1 env_param=0 env_other=0 plain=0",
		},
		{
			// The fact the whole census turns on: a lambda that is only ever
			// CALLED produces no indirect dispatch, so there is nothing here to
			// devirtualise. If this ever counts above zero, try_lift_binding has
			// stopped direct-calling called-only lambdas and #6638's
			// defunctionalise row is worth reopening.
			name: "called-only-lambda",
			src: `function main(): i32 {
    var n: i32 = 10;
    var f: (i32) => i32 = function(x: i32): i32 { return x + n; };
    return f(5);
}`,
			want: "env=0 env_local=0 env_call=0 env_param=0 env_other=0 plain=0",
		},
		{
			// Same lambda, now ALSO passed to a higher-order callee. Escaping
			// blocks the direct call, so the local call site becomes a real
			// indirect dispatch through a flat box that names one target — the
			// residual env_local population, 2 sites corpus-wide.
			name: "escaping-and-called",
			src: `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
    var inc: (i32) => i32 = function(x: i32): i32 { return x + 1; };
    if (inc(41) != 42) { return 14; }
    if (apply(inc, 100) != 101) { return 17; }
    return 0;
}`,
			want: "env=2 env_local=1 env_call=0 env_param=1 env_other=0 plain=0",
		},
		{
			// A CAPTURING lambda in the same shape. The capture puts the box
			// behind a provenance the census does not name, which is the honest
			// answer: env_other is what no rewrite short of a points-to analysis
			// can resolve, and the capturing spelling lands there while the
			// non-capturing one above does not.
			name: "capturing-escapes-and-called",
			src: `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
    var n: i32 = 10;
    var addn: (i32) => i32 = function(x: i32): i32 { return x + n; };
    return addn(5) + apply(addn, 1);
}`,
			want: "env=2 env_local=0 env_call=0 env_param=1 env_other=1 plain=0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := censusLine(t, bin, tc.src)
			// A bailed function contributes no ops, so a census over a module
			// that bailed counts nothing and would pass every assertion below
			// by vacuity.
			if got := censusField(t, line, "bail"); got != 0 {
				t.Fatalf("%d function(s) bailed out of the lowered subset, so the census saw an incomplete module: %s", got, line)
			}
			if !strings.HasSuffix(line, tc.want) {
				t.Errorf("census = %q\n   want it to end %q", line, tc.want)
			}
		})
	}
}

// TestSelfHostClosureCallCensusPopulation sweeps the conformance corpus and the
// stdlib and holds the closure-call population to the size that decided #6638's
// defunctionalise row.
//
// The ceilings are deliberately loose — this is not a golden. What it catches is
// the population changing CLASS: env_local rising means called-only lambdas stopped
// being direct-called, and a large jump in the total means Fern code started
// dispatching through closures at a rate the "not worth a pass" conclusion was
// measured against and no longer covers.
func TestSelfHostClosureCallCensusPopulation(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	root := langSrcAbs(t, "")
	var files []string
	for _, pat := range []string{
		filepath.Join(root, "conformance", "cases", "*", "main.fern"),
		filepath.Join(root, "internal", "stdlib", "std", "*.fern"),
		filepath.Join(root, "internal", "stdlib", "std", "*", "*.fern"),
	} {
		got, err := filepath.Glob(pat)
		if err != nil {
			t.Fatalf("globbing %s: %v", pat, err)
		}
		files = append(files, got...)
	}
	if len(files) < 450 {
		t.Fatalf("swept %d files, expected the full corpus + stdlib — a silently shrunken sweep proves nothing", len(files))
	}

	totals := map[string]int{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		line := censusLine(t, bin, string(src))
		for _, k := range []string{"fns", "env", "env_local", "env_call", "env_param", "env_other", "plain"} {
			totals[k] += censusField(t, line, k)
		}
	}
	t.Logf("closure-call census over %d modules: fns=%d env=%d (local=%d call=%d param=%d other=%d) plain=%d",
		len(files), totals["fns"], totals["env"], totals["env_local"], totals["env_call"],
		totals["env_param"], totals["env_other"], totals["plain"])

	if totals["fns"] < 2500 {
		t.Errorf("census saw %d lowered functions, expected the full set — a shrunken sweep proves nothing", totals["fns"])
	}
	if totals["env"] > 200 {
		t.Errorf("closure-call population is %d env-first sites, measured at 91 — #6638's decision to skip defunctionalise was sized against that number and needs re-deciding", totals["env"])
	}
	if totals["env_local"] > 10 {
		t.Errorf("env_local is %d, measured at 2 — a locally decidable closure call means try_lift_binding stopped direct-calling a lambda it used to, which is a lowering regression before it is a census one", totals["env_local"])
	}
}
