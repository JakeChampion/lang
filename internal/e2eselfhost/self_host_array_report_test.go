package e2eselfhost

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

const selfHostArrayReportSrc = `import "std/array";
@noinline function maps(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64)).map((x: i64): i64 => x * (2 as i64)).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function filtered(xs: i64[]): i64 {
  return xs.filter((x: i64): boolean => x > (1 as i64)).map((x: i64): i64 => x * (2 as i64)).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function own_map(own xs: i64[]): i64[] {
  return xs.map((x: i64): i64 => x + (1 as i64));
}
@noinline function shared(xs: i64[]): i64 {
  let ys: i64[] = xs.map((x: i64): i64 => x + (1 as i64));
  return ys.fold(0 as i64, (a: i64, b: i64): i64 => a + b) + (ys.len() as i64);
}
@noinline function unresolved(xs: i64[], f: (i64) => i64): i64 {
  return xs.map(f).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function effectful(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => { print("element"); return x; }).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function unsupported(xs: i64[]): i64 {
  return xs.scan(0 as i64, (a: i64, b: i64): i64 => a + b).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function boundary(xs: i64[]): i64 {
  let ys: i64[] = xs.map((x: i64): i64 => x);
  print("between");
  return ys.fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function control(xs: i64[]): i64 {
  let ys: i64[] = xs.map((x: i64): i64 => x);
  if (xs.len() > 1000) { return 0 as i64; }
  return ys.fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function nonscalar(xs: string[]): i64 {
  return xs.map((x: string): i64 => x.len() as i64).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function lone(xs: i64[]): i64 {
  return xs.fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function length(xs: i64[]): i32 {
  return xs.map((x: i64): i64 => x).len();
}
function main(): i32 {
  let xs: i64[] = [1 as i64, 2 as i64, 3 as i64];
  if (maps(xs) != 18 as i64 || filtered(xs) != 10 as i64) { return 1; }
  let donor: i64[] = xs;
  donor = own_map(donor);
  if (donor[2] != 4 as i64 || xs[2] != 3 as i64) { return 2; }
  if (shared(xs) != 12 as i64 || unresolved(xs, (x: i64): i64 => x) != 6 as i64) { return 3; }
  if (effectful(xs) != 6 as i64 || unsupported(xs) != 10 as i64) { return 4; }
  if (boundary(xs) != 6 as i64 || control(xs) != 6 as i64) { return 5; }
  if (nonscalar(["one", "two"]) != 6 as i64 || lone(xs) != 6 as i64 || length(xs) != 3) { return 6; }
  return 0;
}
`

func TestSelfHostArrayReportMatchesEmission(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	dir := e2eharness.WriteSourceModloadProject(t, selfHostArrayReportSrc)
	compile := func(report, disable, noReuse string) (string, string) {
		t.Helper()
		cmd := runX86_64Bin(runner, driver, filepath.Join(dir, "main.fern"))
		cmd.Env = append(os.Environ(), "FERN_ARRAY_REPORT="+report, "FERN_NO_ARRAY_FUSION="+disable, "FERN_SELFHOST_NO_REUSE="+noReuse)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		asm, err := cmd.Output()
		if err != nil {
			t.Fatalf("compile: %v\n%s", err, stderr.String())
		}
		return string(asm), stderr.String()
	}
	asm, report := compile("1", "0", "0")
	quietASM, quiet := compile("0", "0", "0")
	if quiet != "" || quietASM != asm {
		t.Fatal("reporting changed emitted code or printed while disabled")
	}
	if !strings.Contains(report, "array report: primary Fern compiler") {
		t.Fatalf("missing compiler identity:\n%s", report)
	}
	for _, tc := range []struct{ name, plan, reason string }{
		{"maps", "map -> map -> fold; one traversal", "fused"},
		{"filtered", "filter -> map -> fold; one traversal", "fused"},
		{"own_map", "map; not fused", "result-escapes"},
		{"shared", "fold; not fused", "intermediate-shared"},
		{"unresolved", "map -> fold; not fused", "element-fn-unresolved"},
		{"effectful", "map -> fold; not fused", "element-fn-effectful"},
		{"unsupported", "fold; not fused", "operator-outside-algebra"},
		{"boundary", "map -> fold; not fused", "effect-boundary"},
		{"control", "fold; not fused", "control-flow-boundary"},
		{"nonscalar", "map -> fold; not fused", "non-scalar"},
		{"lone", "fold; not fused", "no-producer"},
		{"length", "map; not fused", "no-reduction-sink"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := strings.Index(report, tc.name+": "+tc.plan+"\n")
			if start < 0 {
				t.Fatalf("missing plan %s: %s\n%s", tc.name, tc.plan, report)
			}
			diagnostic := strings.SplitN(report[start:], "\n", 3)[1]
			if !strings.Contains(diagnostic, "reason="+tc.reason+" ") || !strings.Contains(diagnostic, "stage=") {
				t.Fatalf("wrong decision: %s", diagnostic)
			}
			body := emittedBody(t, asm, "__fn_"+tc.name)
			retained := strings.Contains(body, "call __fn___arrm_") || strings.Contains(body, "call __fn_array__")
			if tc.name == "own_map" {
				if retained || !strings.Contains(body, "cmpl $1, -8(") || !strings.Contains(body, "call __fn___fern_arr_slice") {
					t.Fatalf("missing guarded reuse with shared copy:\n%s", body)
				}
				return
			}
			if retained == (tc.reason == "fused") {
				t.Fatalf("reported %s but retained=%t\n%s", tc.reason, retained, body)
			}
		})
	}
	start := strings.Index(report, "own_map: map; ownership lowering\n")
	if start < 0 || !strings.Contains(strings.SplitN(report[start:], "\n", 3)[1], "reason=guarded-reuse storage=unique-reuse/shared-copy") {
		t.Fatalf("missing final ownership decision:\n%s", report)
	}
	bin := buildBin(t, gcc, dir, "array-report", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("reported program: %v\n%s", err, out)
	}
	_, disabled := compile("1", "1", "0")
	if strings.Contains(disabled, "reason=fused ") || !strings.Contains(disabled, "reason=disabled ") {
		t.Fatalf("disabled fusion reported a rewrite:\n%s", disabled)
	}
	_, noReuse := compile("1", "0", "1")
	if strings.Contains(noReuse, "reason=guarded-reuse ") || !strings.Contains(noReuse, "reason=reuse-disabled ") {
		t.Fatalf("disabled reuse reported a rewrite:\n%s", noReuse)
	}
}

func TestSelfHostArrayReportValues(t *testing.T) {
	t.Setenv("FERN_ARRAY_REPORT", "1")
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			if out, code := runSelfHostFusionProgram(t, target, selfHostArrayReportSrc); code != 0 {
				t.Fatalf("reported program exited %d\n%s", code, out)
			}
		})
	}
}
