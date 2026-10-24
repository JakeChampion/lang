package e2ecompiler

import (
	"os/exec"
	"testing"
)

const f64ToStringProgram = `import "std/f64";
function main(): i32 {
    print((3.5 as f64).to_string());
    print((0.0 as f64 - 2.25).to_string());
    print((0.0 as f64).to_string());
    print((1.0 as f64).to_string());
    print((100.0 as f64).to_string());
    print((123456.789 as f64).to_string());
    print((0.1 as f64).to_string());
    print((0.5 as f64).to_string());
    print((9999999.99 as f64).to_string());
    print((0.0 as f64 - 0.000125).to_string());
    print((1.0 / 3.0).to_string());
    return 0;
}`

const f64ToStringWant = "3.5\n" +
	"-2.25\n" +
	"0\n" +
	"1\n" +
	"100\n" +
	"123456.789\n" +
	"0.1\n" +
	"0.5\n" +
	"9999999.99\n" +
	"-0.000125\n" +
	"0.3333333333333333\n"

func TestSelfHostF64ToStringX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	asm, progDir := compileSourceModload(t, runner, driverBin, f64ToStringProgram)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, progDir, "f64prog", asm)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	out, _ := cmd.Output()
	if string(out) != f64ToStringWant {
		t.Errorf("f64.to_string output mismatch:\n got: %q\nwant: %q", string(out), f64ToStringWant)
	}
}

func TestSelfHostF64ToStringArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	asm, progDir := compileSourceModload(t, x86runner, driverBin, f64ToStringProgram, "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the f64 program")
	}
	f64Bin := buildBin(t, arm64gcc, progDir, "f64prog", asm)

	cmd := runArm64Bin(qemu, f64Bin)
	out, _ := cmd.Output()
	if string(out) != f64ToStringWant {
		t.Errorf("f64.to_string (arm64) output mismatch:\n got: %q\nwant: %q", string(out), f64ToStringWant)
	}
}
