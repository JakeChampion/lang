package coreutils

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestWasmSmoke(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Fatalf("wasmtime is not on PATH: the wasm32-wasi smoke gate cannot run, and a SKIP here would report a target nobody exercised")
	}
	fern := e2eharness.BuildLangBinForInterp(t)
	dir := t.TempDir()
	for name, content := range map[string]string{"in.txt": "x\ny\n", "in2.txt": "x\nz\n", "in3.txt": "w\ny\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wasmDir := t.TempDir()
	built := map[string]string{}
	wasmBin := func(util string) string {
		if bin, ok := built[util]; ok {
			return bin
		}
		src := utilSource(t, util)
		e2eharness.TrackFernSources(t, filepath.Dir(src), util+".fern")
		bin := filepath.Join(wasmDir, util+".wasm")
		cmd := exec.Command(fern, "-target", "wasm32-wasi", "-o", bin, src)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile %s for wasm32-wasi: %v\n%s", util, err, out)
		}
		built[util] = bin
		return bin
	}

	// A case with `wasm` set needs a process, which WASI does not give. On
	// wasm it must fail with that diagnostic and write nothing.
	cases := []struct {
		util    string
		args    []string
		stdin   string
		preopen bool
		wasm    string
	}{
		{util: "true", args: []string{"--version"}},
		{util: "true", args: []string{"--", "--version"}},
		{util: "false"},
		{util: "echo", args: []string{"hello", "wasm"}},
		{util: "basename", args: []string{"a/b/c.txt", ".txt"}},
		{util: "cat", args: []string{"-n"}, stdin: "x\ny\n"},
		{util: "cat", args: []string{"in.txt", "-"}, stdin: "z\n", preopen: true},
		{util: "cat", args: []string{"nosuch"}, preopen: true},
		{util: "head", args: []string{"-n", "2"}, stdin: "a\nb\nc\n"},
		{util: "head", args: []string{"-n", "1", "in.txt"}, preopen: true},
		{util: "head", args: []string{"nosuch"}, preopen: true},
		{util: "head", args: []string{"--", "--version"}},
		{util: "wc", stdin: "a\nb\n"},
		{util: "wc", args: []string{"in.txt", "-"}, stdin: "q\n", preopen: true},
		{util: "wc", args: []string{"-l", "nosuch"}},
		{util: "dd", args: []string{"if=in.txt", "of=ddout", "bs=2", "status=noxfer"}, preopen: true},
		{util: "dd", args: []string{"bs=3", "status=noxfer"}, stdin: "abcdefg"},
		{util: "dd", args: []string{"if=nosuch", "status=noxfer"}, preopen: true},
		{util: "sed", args: []string{"s/x/X/;$="}, stdin: "x\ny\n"},
		{util: "sed", args: []string{"-n", "-E", "s/(y)/[\\1]/p", "in.txt"}, preopen: true},
		{util: "sed", args: []string{"y/xy/XY/;l", "in.txt", "nosuch"}, preopen: true},
		{util: "find", args: []string{".", "-name", "in*", "-printf", "%f %s %y %d\n"}, preopen: true},
		{util: "find", args: []string{".", "-type", "f", "-size", "-1k", "-o", "-type", "d", "-print"}, preopen: true},
		{util: "find", args: []string{"in.txt", "nosuch", "-print0"}, preopen: true},
		{util: "find", args: []string{".", "-name"}},
		{util: "diff", args: []string{"in.txt", "in2.txt"}, preopen: true},
		{util: "diff", args: []string{"-u", "--label", "a", "--label", "b", "in.txt", "in2.txt"}, preopen: true},
		{util: "diff", args: []string{"-y", "-W", "20", "in.txt", "in2.txt"}, preopen: true},
		{util: "diff", args: []string{"-DX", "in.txt", "-"}, stdin: "x\nq\n", preopen: true},
		{util: "diff", args: []string{"-l", "in.txt", "in2.txt"}, preopen: true, wasm: "diff: subsidiary program '/usr/bin/pr' could not be invoked\n"},
		{util: "cmp", args: []string{"in.txt", "in2.txt"}, preopen: true},
		{util: "diff3", args: []string{"in.txt", "in2.txt", "in3.txt"}, preopen: true},
		{util: "diff3", args: []string{"-m", "in.txt", "in2.txt", "in3.txt"}, preopen: true},
		{util: "diff3", args: []string{"--diff-program=diff", "in.txt", "in2.txt", "in3.txt"}, preopen: true, wasm: "diff3: subsidiary program 'diff' could not be invoked\n"},
		{util: "diff3", args: []string{"in.txt", "in2.txt", "nosuch"}, preopen: true},
		{util: "sdiff", args: []string{"-w", "20", "in.txt", "in2.txt"}, preopen: true},
		{util: "sdiff", args: []string{"-w", "20", "-o", "merged", "in.txt", "in2.txt"}, stdin: "r\n", preopen: true},
		{util: "sdiff", args: []string{"-o", "merged", "-I", "[", "in.txt", "in2.txt"}, preopen: true, wasm: "diff: [: Invalid regular expression\n"},
	}
	for _, c := range cases {
		name := c.util + " " + strings.Join(c.args, " ")
		t.Run(name, func(t *testing.T) {
			inv := invocation{args: c.args, stdin: c.stdin, dir: dir}
			want := inv.run(t, fernBin(t, c.util), c.util)

			argv := []string{"run", "--argv0", c.util}
			if c.preopen {
				argv = append(argv, "--dir="+dir)
			}
			argv = append(append(argv, wasmBin(c.util)), c.args...)
			cmd := exec.Command(wasmtime, argv...)
			cmd.Dir = dir
			cmd.Env = append(baseEnv(), "HOME="+os.Getenv("HOME"))
			cmd.Stdin = strings.NewReader(c.stdin)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			exit := 0
			if ee, ok := err.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("wasmtime: %v", err)
			}
			if c.wasm != "" {
				if exit == 0 || stdout.Len() != 0 || stderr.String() != c.wasm {
					t.Errorf("%s %s on wasm: exit %d, stdout %q, stderr %q; want a failure with %q", c.util, quoteArgs(c.args), exit, stdout.String(), stderr.String(), c.wasm)
				}
				return
			}
			if !bytes.Equal(want.stdout, stdout.Bytes()) {
				t.Errorf("stdout differs for %s %s%s", c.util, quoteArgs(c.args), diffBody("native", "wasm", want.stdout, stdout.Bytes()))
			}
			if !bytes.Equal(want.stderr, stderr.Bytes()) {
				t.Errorf("stderr differs for %s %s%s", c.util, quoteArgs(c.args), diffBody("native", "wasm", want.stderr, stderr.Bytes()))
			}
			if (want.exit != 0) != (exit != 0) || want.signal != "" {
				t.Errorf("status differs for %s %s: native %s, wasm exit %d", c.util, quoteArgs(c.args), want.how(), exit)
			}
		})
	}
}
