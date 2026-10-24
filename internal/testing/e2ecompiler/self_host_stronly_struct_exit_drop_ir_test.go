package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var strOnlyStructExitDropCases = []struct {
	name string
	src  string
	want int
}{
	{"stronly-exit-flat", `struct P { s: string, n: i32 }
function go(pre: string): i32 { let p: P = P { s: pre + "x", n: 1 }; return p.n + p.s.len(); }
function churn(m: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
	{"stronly-borrow-arg-flat", `struct P { s: string, n: i32 }
function rd(p: P): i32 { return p.n + p.s.len(); }
function go(pre: string): i32 { let p: P = P { s: pre + "x", n: 1 }; return rd(p); }
function churn(m: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
	{"stronly-param-embed-safe", `struct P { s: string, n: i32 }
function go(pre: string): i32 { let p: P = P { s: pre, n: 1 }; return p.n + p.s.len(); }
function churn(m: i32): i32 { let pre: string = "ab" + "cd"; let bad: i32 = 0; let i: i32 = 0; while (i < m) { if (go(pre) != 5) { bad = 1; } i = i + 1; } if (pre.len() != 4) { bad = 2; } return bad; }
function main(): i32 { let v: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
	{"stronly-loop-rebind-flat", `struct P { s: string, n: i32 }
function main(): i32 {
    let pre: string = "ab";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) { let p: P = P { s: pre + "x", n: i }; acc = (acc + p.n + p.s.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 3000) { let p2: P = P { s: pre + "x", n: j }; acc = (acc + p2.n + p2.s.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"stronly-closure-capture-flat", `struct P { s: string, n: i32 }
function go(pre: string): i32 {
    let p: P = P { s: pre + "x", n: 1 };
    let c = () => p.n + p.s.len();
    return c();
}
function churn(m: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
}

func TestSelfHostStrOnlyStructExitDropIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range strOnlyStructExitDropCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = string field leaked; 99 = over-release/underflow; 97 = value corrupted; 1/2 = live value lost)", tc.name, code, tc.want)
			}
		})
	}
}
