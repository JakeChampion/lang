package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- `s[i]` is bounds-checked on the self-host wasm tier too --------------
//
// The other two tiers refuse an out-of-range string index:
//
//	interp        exit 1    string index 99 out of range [0, 3)
//	native x86-64 exit 134  fern: string index out of range
//
// The self-host wasm emitter did neither. `str_index` was a plain arm of
// emit_str_op_wat — `i32.const 4; i32.add; i32.add; i32.load8_u` — which forms
// the address from an unchecked index and reads whatever is there. A negative
// index reads below the block; a large one reads past it. No trap, no
// diagnostic, a plausible byte returned (#8483).
//
// The array ops on this tier have had the check all along, and a wasm string
// block is `[len@0][bytes@4]` — the same length prefix an array carries — so
// wasm_arr_bounds_check applies unchanged. What str_index lacked was not the
// check but ACCESS to it: emit_str_op_wat is a plain Op-to-string with no
// scratch locals, and the check needs one to hold the index across the
// compare. Moving the arm to the dispatch site, where arr_get already reads
// arrtmp / bctmp, is the whole fix.
//
// The rows assert the process DIES rather than asserting a particular byte:
// what is read out of bounds is not defined, so a test that pinned a value
// would pass for the wrong reason on a different heap layout.
func TestSelfHostStrIndexBoundsWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm string-bounds e2e")
	}
	gcc, _ := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := cachedDriverBin(t, gcc, dir, "wasm_ir_run.fern")

	cases := []struct {
		name    string
		src     string
		wantOK  bool // true: runs to completion with wantOut
		wantOut string
	}{
		{
			name: "index past the end traps",
			src: `function main(): i32 {
  var s: string = "abc";
  var i: i32 = 99;
  var b: u8 = s[i];
  if (b == 0) { print("zero"); } else { print("nonzero"); }
  return 0;
}`,
		},
		{
			name: "negative index traps",
			src: `function main(): i32 {
  var s: string = "abc";
  var i: i32 = 0 - 1;
  var b: u8 = s[i];
  if (b == 0) { print("zero"); } else { print("nonzero"); }
  return 0;
}`,
		},
		{
			// The index equal to the length is the boundary the check must
			// exclude: `abc` has valid indices 0..2.
			name: "index equal to the length traps",
			src: `function main(): i32 {
  var s: string = "abc";
  var i: i32 = 3;
  var b: u8 = s[i];
  if (b == 0) { print("zero"); } else { print("nonzero"); }
  return 0;
}`,
		},
		{
			// In-range reads must be untouched, including the last valid index.
			name: "in range still reads",
			src: `function main(): i32 {
  var s: string = "abc";
  var i: i32 = 2;
  var b: u8 = s[i];
  if (b == 99) { print("c"); } else { print("other"); }
  return 0;
}`,
			wantOK:  true,
			wantOut: "c\n",
		},
		{
			name: "first byte still reads",
			src: `function main(): i32 {
  var s: string = "abc";
  var i: i32 = 0;
  var b: u8 = s[i];
  if (b == 97) { print("a"); } else { print("other"); }
  return 0;
}`,
			wantOK:  true,
			wantOut: "a\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(driverBin, "-ir")
			cmd.Stdin = bytes.NewReader([]byte(c.src))
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			var emitErr strings.Builder
			cmd.Stderr = &emitErr
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("self-host wasm emit failed: %v\n%s", err, emitErr.String())
			}
			watFile := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_")+".wat")
			if werr := os.WriteFile(watFile, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			run := exec.Command("wasmtime", "run", watFile)
			var out, runErr strings.Builder
			run.Stdout, run.Stderr = &out, &runErr
			_ = run.Run()
			exit := -1
			if run.ProcessState != nil {
				exit = run.ProcessState.ExitCode()
			}
			if c.wantOK {
				if exit != 0 {
					t.Fatalf("in-range read did not complete: exit %d\n%s", exit, runErr.String())
				}
				if out.String() != c.wantOut {
					t.Errorf("stdout = %q, want %q", out.String(), c.wantOut)
				}
				return
			}
			if exit == 0 {
				t.Errorf("an out-of-range string index ran to completion and printed %q.\n"+
					"interp exits 1 and native x86-64 aborts 134 for this program; the wasm tier "+
					"must not silently read past the block (#8483).", out.String())
			}
		})
	}
}
