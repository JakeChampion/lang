package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostLambdaMangleX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	lamlib := `pub function add_one(x: i32): i32 { return x + 1; }

pub function apply(f: (i32) => i32, x: i32): i32 { return f(x); }

pub function via_bare(x: i32): i32 { return apply(add_one, x); }

pub function via_lambda(x: i32): i32 {
    return apply((v: i32): i32 => { return add_one(v) + 10; }, x);
}

pub function shadow_param(x: i32): i32 {
    return apply((add_one: i32): i32 => { return add_one * 2; }, x);
}

pub function shadow_local(x: i32): i32 {
    return apply((v: i32): i32 => { let add_one: i32 = 7; return v + add_one; }, x);
}

pub function shadow_capture(x: i32): i32 {
    let add_one: i32 = 50;
    return apply((v: i32): i32 => { return v + add_one; }, x);
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
