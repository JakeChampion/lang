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
