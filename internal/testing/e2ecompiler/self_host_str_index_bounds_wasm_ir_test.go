package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostStrIndexBoundsWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm string-bounds e2e")
	}
	gcc, _ := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := cachedDriverBin(t, gcc, dir, "drivers/wasm_ir_run.fern")

	cases := []struct {
		name    string
		src     string
		wantOK  bool
		wantOut string
	}{
		{
			name: "index past the end traps",
			src: `function main(): i32 {
  let s: string = "abc";
  let i: i32 = 99;
  let b: u8 = s[i];
  if (b == 0) { print("zero"); } else { print("nonzero"); }
  return 0;
}`,
		},
		{
			name: "negative index traps",
			src: `function main(): i32 {
  let s: string = "abc";
  let i: i32 = 0 - 1;
  let b: u8 = s[i];
  if (b == 0) { print("zero"); } else { print("nonzero"); }
  return 0;
}`,
		},
		{
			name: "index equal to the length traps",
			src: `function main(): i32 {
  let s: string = "abc";
  let i: i32 = 3;
  let b: u8 = s[i];
  if (b == 0) { print("zero"); } else { print("nonzero"); }
  return 0;
}`,
		},
		{
			name: "in range still reads",
			src: `function main(): i32 {
  let s: string = "abc";
  let i: i32 = 2;
  let b: u8 = s[i];
  if (b == 99) { print("c"); } else { print("other"); }
  return 0;
}`,
			wantOK:  true,
			wantOut: "c\n",
		},
		{
			name: "first byte still reads",
			src: `function main(): i32 {
  let s: string = "abc";
  let i: i32 = 0;
  let b: u8 = s[i];
  if (b == 97) { print("a"); } else { print("other"); }
  return 0;
}`,
			wantOK:  true,
			wantOut: "a\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(driverBin)
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
