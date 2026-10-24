package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var iterCombinatorIRCases = []struct {
	name string
	src  string
}{
	{"find-named", `import "core/iter";
function gt1(x: i32): boolean { return x > 1; }
function main(): i32 { let xs: i32[] = [1, 2, 3]; match (iter.of(xs).find(gt1)) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"find-named-none", `import "core/iter";
function big(x: i32): boolean { return x > 100; }
function main(): i32 { let xs: i32[] = [1, 2, 3]; match (iter.of(xs).find(big)) { Some(v) => { return v; }, None => { return 7; } } }`},
	{"find-lambda", `import "core/iter";
function main(): i32 { match (iter.of([1, 2, 3]).find((x: i32): boolean => { return x >= 2; })) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"any", `import "core/iter";
function gt2(x: i32): boolean { return x > 2; }
function main(): i32 { let xs: i32[] = [1, 2, 3]; if (iter.of(xs).any(gt2)) { return 1; } return 0; }`},
	{"all", `import "core/iter";
function gt0(x: i32): boolean { return x > 0; }
function main(): i32 { let xs: i32[] = [1, 2, 3]; if (iter.of(xs).all(gt0)) { return 1; } return 0; }`},
	{"position", `import "core/iter";
function gt1(x: i32): boolean { return x > 1; }
function main(): i32 { let xs: i32[] = [1, 2, 3]; match (iter.position(iter.of(xs), gt1)) { Some(v) => { return v; }, None => { return 9; } } }`},
	{"count_where", `import "core/iter";
function gt1(x: i32): boolean { return x > 1; }
function main(): i32 { let xs: i32[] = [1, 2, 3]; return iter.count_where(iter.of(xs), gt1); }`},
	{"flat_map", `import "core/iter";
function dup(x: i32): i32[] { return [x, x]; }
function main(): i32 { let xs: i32[] = [1, 2]; let ys: i32[] = iter.of(xs).flat_map(dup).collect(); return ys.len(); }`},
	{"enumerate", `import "core/iter";
function main(): i32 { let xs: i32[] = [5, 6, 7]; let ys: (i32, i32)[] = iter.of(xs).enumerate().collect(); return ys.len(); }`},
	{"zip", `import "core/iter";
function main(): i32 { let a: i32[] = [1, 2]; let b: i32[] = [3, 4]; let ys: (i32, i32)[] = iter.of(a).zip(iter.of(b)).collect(); return ys.len(); }`},
	{"take", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; let ys: i32[] = iter.of(xs).take(2).collect(); return ys.len(); }`},
	{"skip", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; let ys: i32[] = iter.of(xs).skip(2).collect(); return ys.len(); }`},
	{"nth", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; match (iter.nth(iter.of(xs), 2)) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"last", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; match (iter.last(iter.of(xs))) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"index_of", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; match (iter.index_of(iter.of(xs), 3)) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"count", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 2, 3]; return iter.count(iter.of(xs), 2); }`},
}

func TestSelfHostIterCombinatorsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range iterCombinatorIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "itercomb_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "itercomb_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
