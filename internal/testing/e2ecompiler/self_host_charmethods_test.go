package e2ecompiler

import (
	"os/exec"
	"testing"
)

var charMethodCases = []struct {
	name string
	src  string
	exit int
}{
	{"to_lower", "import \"std/i32\"; function main(): i32 { let c: u8 = 65; return c.to_ascii_lower() as i32; }", 97},
	{"to_lower-noop", "import \"std/i32\"; function main(): i32 { let c: u8 = 53; return c.to_ascii_lower() as i32; }", 53},
	{"to_upper", "import \"std/i32\"; function main(): i32 { let c: u8 = 122; return c.to_ascii_upper() as i32; }", 90},
	{"is_digit", "import \"std/i32\"; function main(): i32 { let c: u8 = 53; if (c.is_ascii_digit()) { return 1; } return 0; }", 1},
	{"is_digit-false", "import \"std/i32\"; function main(): i32 { let c: u8 = 65; if (c.is_ascii_digit()) { return 1; } return 0; }", 0},
	{"is_alpha-upper", "import \"std/i32\"; function main(): i32 { let c: u8 = 90; if (c.is_ascii_alpha() && c.is_ascii_upper() && !c.is_ascii_lower()) { return 1; } return 0; }", 1},
	{"is_hex-alnum", "import \"std/i32\"; function main(): i32 { let c: u8 = 102; if (c.is_ascii_hex_digit() && c.is_ascii_alnum()) { return 1; } return 0; }", 1},
	{"punct-neither", "import \"std/i32\"; function main(): i32 { let c: u8 = 35; if (c.is_ascii_alnum() || c.is_ascii_hex_digit()) { return 1; } return 0; }", 0},
	{"to_ascii_string", "import \"std/i32\"; function main(): i32 { let c: u8 = 65; return c.to_ascii_string()[0] as i32; }", 65},
}

func TestSelfHostCharMethodsX86_64(t *testing.T) {
	l := newStdlibLoader(t)
	for _, tc := range charMethodCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := l.emit(t, tc.src)
			cmd := runX86_64Bin(l.runner, buildBin(t, l.gcc, t.TempDir(), tc.name, asm))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostSortX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	main := "import \"core/cmp\";\n" +
		"function main(): i32 { let r = cmp.sort_in_place([5, 2, 8, 1, 9, 3]); return r[0] * 100 + r[5]; }\n"
	asm, progDir := compileSourceModload(t, runner, driverBin, main)
	progBin := buildBin(t, gcc, progDir, "sortprog", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 109 {
		t.Errorf("sort_in_place result exited %d, want 109 (min=1, max=9)", code)
	}
}

func TestSelfHostCharMethodsArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range charMethodCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}

func TestSelfHostSortArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)
	main := "import \"core/cmp\";\n" +
		"function main(): i32 { let r = cmp.sort_in_place([5, 2, 8, 1, 9, 3]); return r[0] * 100 + r[5]; }\n"
	asm, progDir := compileSourceModload(t, x86runner, driverBin, main, "-target", "arm64-linux")
	progBin := buildBin(t, arm64gcc, progDir, "sortprog", asm)
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 109 {
		t.Errorf("sort_in_place result exited %d, want 109", code)
	}
}
