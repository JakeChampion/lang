package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var lambdaFnSidecarCases = []struct {
	name  string
	files map[string]string
	want  int
}{
	{"generic-clone-substitutes-the-pair", map[string]string{
		"main.fern": `pub function make[T](seed: T): T {
    function idmaker(base: T): (T) => T {
        return (x: T) => x;
    }
    let f: (T) => T = idmaker(seed);
    return f(seed);
}

function main(): i32 {
    return make(7);
}
`,
	}, 7},

	{"flatten-mangles-the-pair", map[string]string{
		"bmod.fern": `pub struct Box { v: i32 }

pub function mk(): i32 {
    function pick(b: Box): (Box) => i32 {
        return (x: Box) => x.v;
    }
    let g: (Box) => i32 = pick(Box { v: 3 });
    return g(Box { v: 4 });
}
`,
		"main.fern": `import "./bmod";

function main(): i32 {
    return bmod.mk();
}
`,
	}, 4},

	{"recursive-local-hoist-keeps-the-pair", map[string]string{
		"main.fern": `function main(): i32 {
    function rec(n: i32): (i32) => i32 {
        if (n <= 0) { return (x: i32) => x; }
        return rec(n - 1);
    }
    let f: (i32) => i32 = rec(2);
    return f(9);
}
`,
	}, 9},
}

func TestSelfHostLambdaFnSidecarCensus(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("census driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/semsource_census_run.fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	census := filepath.Join(dir, "census")
	build := exec.Command(buildFernCLIBin(t), "-target", "x86-64-linux",
		"-embed", stdlib, "-o", census, filepath.Join(dir, "drivers/semsource_census_run.fern"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the census failed: %v\n%s", err, out)
	}

	for _, tc := range lambdaFnSidecarCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			progDir := t.TempDir()
			for name, src := range tc.files {
				if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			out, err := exec.Command(census, filepath.Join(progDir, "main.fern")).CombinedOutput()
			if err != nil {
				t.Fatalf("census failed: %v\n%s", err, out)
			}
			report := string(out)
			t.Logf("census:\n%s", report)
			if strings.Contains(report, "unresolved result type") {
				t.Errorf("the producer has no spelling for the nested function's result — the "+
					"lambda's ret_fn_ret / ret_fn_param_types were dropped or left stale on the "+
					"way here.\n%s", report)
			}
			if strings.Contains(report, "no semantic contract: __lam_") {
				t.Errorf("a hoisted lambda has no contract, which is what an unresolved result "+
					"type leaves behind for its callers.\n%s", report)
			}
		})
	}
}

func TestSelfHostLambdaFnSidecarX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	for _, tc := range lambdaFnSidecarCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			progDir := t.TempDir()
			for name, src := range tc.files {
				if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			asm := string(runDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern")))
			if !strings.Contains(asm, ".Lssa_") {
				t.Fatal("program did not route through the IR path")
			}
			bin := buildBin(t, gcc, progDir, "lambda_fn_sidecar", asm)
			if _, exit := runBin(binCmd(runner, bin), ""); exit != tc.want {
				t.Errorf("exit = %d, want %d (native)", exit, tc.want)
			}
		})
	}
}
