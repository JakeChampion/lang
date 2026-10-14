package main

import (
	"slices"
	"strings"
	"testing"
)

// TestCompileArgsEmitDefault pins when the launcher asks the self-host for
// text: a native compile with no -o prints its assembly, as the Go CLI did,
// while a wasm compile passes through so the self-host refuses binary output
// to stdout.
func TestCompileArgsEmitDefault(t *testing.T) {
	for _, c := range []struct {
		target, out, emit string
		want              string
	}{
		{"x86-64-linux", "", "", "asm"},
		{"arm64-linux", "", "", "asm"},
		{"x86-64-linux", "prog", "", ""},
		{"wasm32-wasi", "", "", ""},
		{"wasm32-wasi-http", "", "", ""},
		{"wasm32-wasi", "", "asm", "asm"},
		{"wasm32-wasi", "", "core-module", "core-module"},
	} {
		args := compileArgs(compileRequest{src: "p.fern", target: c.target, emit: c.emit}, c.out, "stdlib")
		got := ""
		if i := slices.Index(args, "-emit"); i >= 0 {
			got = args[i+1]
		}
		if got != c.want {
			t.Errorf("-target %s -o %q -emit %q: passes -emit %q, want %q (%s)", c.target, c.out, c.emit, got, c.want, strings.Join(args, " "))
		}
	}
}

// TestCompileArgsSize: -size reaches the self-host, and only when asked for.
func TestCompileArgsSize(t *testing.T) {
	sized := compileArgs(compileRequest{src: "p.fern", target: "x86-64-linux", size: true}, "prog", "stdlib")
	plain := compileArgs(compileRequest{src: "p.fern", target: "x86-64-linux"}, "prog", "stdlib")
	if !slices.Contains(sized, "-size") || slices.Contains(plain, "-size") {
		t.Errorf("-size forwarded as %q with the flag and %q without", strings.Join(sized, " "), strings.Join(plain, " "))
	}
}

// TestStdoutRefusal: a wasm compile with no -o is refused unless it asked for
// text, and a native one never is.
func TestStdoutRefusal(t *testing.T) {
	for _, c := range []struct {
		target, out, emit string
		refused           bool
	}{
		{"wasm32-wasi", "", "", true},
		{"wasm32-wasi-http", "", "", true},
		{"wasm32-wasi", "", "core-module", true},
		{"wasm32-wasi", "", "command-module", true},
		{"wasm32-wasi", "", "asm", false},
		{"wasm32-wasi", "prog.wasm", "", false},
		{"x86-64-linux", "", "", false},
	} {
		err := stdoutRefusal(compileRequest{target: c.target, out: c.out, emit: c.emit})
		if (err != nil) != c.refused {
			t.Errorf("-target %s -o %q -emit %q: refused=%v (%v), want %v", c.target, c.out, c.emit, err != nil, err, c.refused)
		}
		if err != nil && (!strings.Contains(err.Error(), "-o OUTPUT") || (c.emit != "" && !strings.Contains(err.Error(), c.emit))) {
			t.Errorf("-target %s -emit %q: %q should name -o OUTPUT and the form", c.target, c.emit, err)
		}
	}
}
