package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var sharedVariantPayloadCases = []struct {
	name string
	src  string
}{
	{"f64-behind-i32", `enum A { W(i32), P }
enum B { W(f64), Q }
function main(): i32 { let b: B = B.W(4.5); match (b) { B.W(n) => { if (n == 4.5) { return 7; } return 2; }, _ => { return 1; } } }`},
	{"f64-behind-i32-match-expr", `enum A { W(i32), P }
enum B { W(f64), Q }
function main(): i32 { let b: B = B.W(4.5); let v: f64 = match (b) { B.W(n) => n, _ => 0.0 }; if (v == 4.5) { return 7; } return 2; }`},
	{"i64-behind-i32", `enum A { W(i32), P }
enum B { W(i64), Q }
function main(): i32 { let b: B = B.W(4294967296); match (b) { B.W(n) => { if (n == 4294967296) { return 9; } return 2; }, _ => { return 1; } } }`},
	{"i64-behind-i32-match-expr", `enum A { W(i32), P }
enum B { W(i64), Q }
function main(): i32 { let b: B = B.W(4294967296); let v: i64 = match (b) { B.W(n) => n, _ => 0 }; if (v == 4294967296) { return 9; } return 2; }`},
	{"string-behind-i32", `enum A { W(i32), P }
enum B { W(string), Q }
function main(): i32 { let b: B = B.W("hello"); match (b) { B.W(n) => { return n.len(); }, _ => { return 1; } } }`},
	{"struct-behind-i32", `struct Pt { x: i32, y: i32 }
enum A { W(i32), P }
enum B { W(Pt), Q }
function main(): i32 { let b: B = B.W(Pt { x: 3, y: 4 }); match (b) { B.W(n) => { return n.x + n.y; }, _ => { return 1; } } }`},
	{"i32-behind-i64", `enum A { W(i64), P }
enum B { W(i32), Q }
function main(): i32 { let b: B = B.W(4); match (b) { B.W(n) => { return n; }, _ => { return 1; } } }`},
	{"payload-behind-unit", `enum A { W, P }
enum B { W(i32), Q }
function main(): i32 { let b: B = B.W(4); match (b) { B.W(n) => { return n; }, _ => { return 1; } } }`},
	{"transposed-multi-payload", `enum A { W(i32, string), P }
enum B { W(string, i32), Q }
function main(): i32 { let b: B = B.W("abcd", 3); match (b) { B.W(s, n) => { return s.len() + n; }, _ => { return 1; } } }`},
	{"unqualified-pattern", `enum A { W(i32), P }
enum B { W(f64), Q }
function main(): i32 { let b: B = B.W(4.5); match (b) { W(n) => { if (n == 4.5) { return 7; } return 2; }, _ => { return 1; } } }`},
	{"param-scrutinee", `enum A { W(i32), P }
enum B { W(f64), Q }
function f(b: B): i32 { match (b) { B.W(n) => { if (n == 4.5) { return 7; } return 2; }, _ => { return 1; } } }
function main(): i32 { return f(B.W(4.5)); }`},
	{"both-enums-matched", `enum A { W(i32), P }
enum B { W(f64), Q }
function main(): i32 { let a: A = A.W(2); let b: B = B.W(4.5); let r: i32 = a_of(a);
    match (b) { B.W(n) => { if (n == 4.5) { r = r + 10; } }, _ => {} }
    return r; }
function a_of(a: A): i32 { match (a) { A.W(n) => { return n; }, _ => { return 0; } } }`},
	{"single-enum-control", `enum S2 { W(f64), Q }
function main(): i32 { let b: S2 = S2.W(4.5); match (b) { S2.W(n) => { if (n == 4.5) { return 7; } return 2; }, _ => { return 1; } } }`},
	{"union-member-control", `struct Num { value: i32 }
struct Word { n: i32 }
type Tok = Num | Word;
function main(): i32 { let t: Tok = Num { value: 6 }; match (t) { Num(x) => { return x.value; }, Word(w) => { return w.n; } } }`},
}

func TestSelfHostSharedVariantPayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range sharedVariantPayloadCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, prog)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
