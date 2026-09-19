package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `parse_type_name` coarsens a function type to the flat tag "fn" and drops the
// signature; `ret_fn_ret` / `ret_fn_param_types` carry it alongside, and
// `fn_tag_spelling` rebuilds the spelling from the pair. `ast.ExprLambda` grew
// the pair so a nested function that RETURNS a function keeps its signature
// through the lambda desugar — but every pass that rebuilds a lambda or hoists
// one to a `FuncDecl` has to carry the pair, and a pass that REWRITES type
// names has to rewrite the pair with them.
//
// Each case is a shape whose nested function returns a function, reached
// through a different one of those passes.
var lambdaFnSidecarCases = []struct {
	name  string
	files map[string]string
	want  int
}{
	// parser.subst_expr clones a generic body per instantiation, substituting
	// the type vars. It substituted `ret_type` and left the pair to the spread,
	// so the clone described its nested function as returning `(T) => T` with
	// `T` unbound in the clone's scope.
	{"generic-clone-substitutes-the-pair", map[string]string{
		"main.fern": `pub function make[T](seed: T): T {
    function idmaker(base: T): (T) => T {
        return (x: T) => x;
    }
    var f: (T) => T = idmaker(seed);
    return f(seed);
}

function main(): i32 {
    return make(7);
}
`,
	}, 7},

	// flatten.rewrite_expr mangles an imported module's type names to their
	// flat form. Same shape: `ret_type` was mangled, the pair was not, so the
	// nested function's signature still named the module-local `Box`.
	{"flatten-mangles-the-pair", map[string]string{
		"bmod.fern": `pub struct Box { v: i32 }

pub function mk(): i32 {
    function pick(b: Box): (Box) => i32 {
        return (x: Box) => x.v;
    }
    var g: (Box) => i32 = pick(Box { v: 3 });
    return g(Box { v: 4 });
}
`,
		"main.fern": `import "./bmod";

function main(): i32 {
    return bmod.mk();
}
`,
	}, 4},

	// parser.hl_rewrite lifts a SELF-RECURSIVE local function to a top-level
	// `FuncDecl` — the sixth lambda-to-`FuncDecl` hoist, and the one the first
	// pass at this missed. It wrote the empty pair.
	{"recursive-local-hoist-keeps-the-pair", map[string]string{
		"main.fern": `function main(): i32 {
    function rec(n: i32): (i32) => i32 {
        if (n <= 0) { return (x: i32) => x; }
        return rec(n - 1);
    }
    var f: (i32) => i32 = rec(2);
    return f(9);
}
`,
	}, 9},
}

// TestSelfHostLambdaFnSidecarCensus is the gate the fix is actually for: a
// dropped or stale pair leaves the checked-source producer with no spelling for
// the nested function's result, and the module refuses at `unresolved result
// type` — which is what the flatten case did, taking its two `__lam_0` callers
// with it as `call target has no semantic contract`.
//
// Only the flatten case discriminates today. The other two reach a LATER
// refusal either way, so the pair they were dropping is not yet what holds them
// back; they are here because a stale spelling is wrong whether or not anything
// currently reads it, and because this is the gate that will notice when
// something does.
func TestSelfHostLambdaFnSidecarCensus(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("census driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "semsource_census_run.fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	census := filepath.Join(dir, "census")
	build := exec.Command(buildFernCLIBin(t), "-target", "x86-64-linux",
		"-embed", stdlib, "-o", census, filepath.Join(dir, "semsource_census_run.fern"))
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

// TestSelfHostLambdaFnSidecarX86_64 checks the same three shapes still RUN
// correctly, against the native compiler's exit codes. A module the producer
// refuses falls back to the AST lowering and runs anyway, so this passes on
// both sides of the fix — it guards the fix from breaking codegen, it does not
// prove it.
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
			if !strings.Contains(asm, ".Lir") {
				t.Fatal("program did not route through the IR path")
			}
			bin := buildBin(t, gcc, progDir, "lambda_fn_sidecar", asm)
			if _, exit := runBin(binCmd(runner, bin), ""); exit != tc.want {
				t.Errorf("exit = %d, want %d (native)", exit, tc.want)
			}
		})
	}
}
