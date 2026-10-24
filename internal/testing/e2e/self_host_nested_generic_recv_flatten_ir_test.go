package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

const stdFlattenSrc = `import "std/option";
import "std/result";
function main(): i32 {
    let x: Option[Option[i32]] = Some(Some(7));
    let a: i32 = match (x.flatten()) { Some(v) => v, None => 0 };
    let y: Option[Option[i32]] = None;
    let b: i32 = match (y.flatten()) { Some(v) => v, None => 100 };
    let r: Result[Result[i32, i32], i32] = Ok(Ok(30));
    let c: i32 = match (r.flatten()) { Ok(v) => v, Err(e) => e };
    let r2: Result[Result[i32, i32], i32] = Err(5);
    let d: i32 = match (r2.flatten()) { Ok(v) => v, Err(e) => e };
    return a + b + c + d;
}
`

func TestStdFlattenNative(t *testing.T) {
	p := writeIterProg(t, stdFlattenSrc)
	if _, code := runFixtureInterp(t, p, ""); code != 142 {
		t.Errorf("std flatten interp = %d, want 142", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 142 {
		t.Errorf("std flatten x86-64 = %d, want 142", code)
	}
	if code := runWasm(t, stdFlattenSrc); code != 142 {
		t.Errorf("std flatten wasm = %d, want 142", code)
	}
}

func TestStdFlattenArm64(t *testing.T) {
	p := writeIterProg(t, stdFlattenSrc)
	if _, code := runFixtureArm64(t, p, ""); code != 142 {
		t.Errorf("std flatten arm64 = %d, want 142", code)
	}
}

const nestedRecvFlattenPrelude = `pub function (o: Option[Option[T]]) flatten(): Option[T] {
    match (o) { Some(inner) => { return inner; }, None => { return None; } }
}
pub function (r: Result[Result[T, E], E]) flatten(): Result[T, E] {
    match (r) { Ok(inner) => { return inner; }, Err(e) => { return Err(e); } }
}
`

var nestedRecvFlattenCases = []struct {
	name string
	main string
	want int
}{
	{"opt-some", `function main(): i32 { let x: Option[Option[i32]] = Some(Some(7)); match (x.flatten()) { Some(v) => { return v; }, None => { return 0; } } }`, 7},
	{"opt-none", `function main(): i32 { let x: Option[Option[i32]] = None; match (x.flatten()) { Some(v) => { return v; }, None => { return 100; } } }`, 100},
	{"res-ok", `function main(): i32 { let r: Result[Result[i32, i32], i32] = Ok(Ok(30)); match (r.flatten()) { Ok(v) => { return v; }, Err(e) => { return e; } } }`, 30},
	{"res-err", `function main(): i32 { let r: Result[Result[i32, i32], i32] = Err(5); match (r.flatten()) { Ok(v) => { return v; }, Err(e) => { return e; } } }`, 5},
}

func nestedRecvFlattenProg(mainBody string) string {
	return nestedRecvFlattenPrelude + mainBody + "\n"
}

func TestNestedRecvFlattenNative(t *testing.T) {
	for _, tc := range nestedRecvFlattenCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, nestedRecvFlattenProg(tc.main))
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, nestedRecvFlattenProg(tc.main)); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostNestedRecvFlattenIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range nestedRecvFlattenCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(nestedRecvFlattenProg(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
