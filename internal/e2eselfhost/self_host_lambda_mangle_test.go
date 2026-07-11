package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostLambdaMangleX86_64 pins the self-host module loader's
// (flatten.fern) handling of lambdas inside a MANGLED module — the
// self-host sibling of the native #4802 fix and of
// TestSelfHostLocalShadowsImportedDecl's top-level shadow rule:
//
//   - a module-local function called from inside a lambda body must be
//     rewritten to its mangled name (flatten already descended into
//     lambda bodies, unlike the native rewriter);
//   - a bare module-local function reference in arg position must be
//     rewritten (flatten's ExprIdent case);
//   - a lambda PARAM or lambda-body LOCAL that shadows a module-level
//     decl must NOT be rewritten (the lambda-scope shadow fix: the
//     body rewrites under ctx.locals extended with the lambda's own
//     bindings);
//   - an ENCLOSING function's local captured by the lambda stays
//     visible and unmangled inside the body.
//
// Compiled through the self-host file-loading modload driver (which
// runs flatten's mangle pass), run natively; exit 0 = every shape
// resolved and computed correctly.
func TestSelfHostLambdaMangleX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	lamlib := `pub function add_one(x: i32): i32 { return x + 1; }

pub function apply(f: (i32) => i32, x: i32): i32 { return f(x); }

pub function via_bare(x: i32): i32 { return apply(add_one, x); }

pub function via_lambda(x: i32): i32 {
    return apply(function (v: i32): i32 { return add_one(v) + 10; }, x);
}

pub function shadow_param(x: i32): i32 {
    return apply(function (add_one: i32): i32 { return add_one * 2; }, x);
}

pub function shadow_local(x: i32): i32 {
    return apply(function (v: i32): i32 { var add_one: i32 = 7; return v + add_one; }, x);
}

pub function shadow_capture(x: i32): i32 {
    var add_one: i32 = 50;
    return apply(function (v: i32): i32 { return v + add_one; }, x);
}
`
	main := `import "./lamlib";
function main(): i32 {
    if (lamlib.via_bare(1) != 2) { return 1; }
    if (lamlib.via_lambda(1) != 12) { return 2; }
    if (lamlib.shadow_param(3) != 6) { return 3; }
    if (lamlib.shadow_local(3) != 10) { return 4; }
    if (lamlib.shadow_capture(3) != 53) { return 5; }
    return 0;
}
`
	asm, progDir := compileFilesModload(t, runner, driverBin, map[string]string{
		"lamlib.fern": lamlib,
		"main.fern":   main,
	})
	progBin := buildBin(t, gcc, progDir, "lambda_mangle", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("lambda-mangle program exited %d, want 0 (1=via_bare 2=via_lambda 3=shadow_param 4=shadow_local 5=shadow_capture)", code)
	}
}
