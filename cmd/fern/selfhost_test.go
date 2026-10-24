package main

import (
	"strings"
	"testing"
)

func TestCompileArgsSize(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		size *bool
		want string
	}{{&on, "-size=true"}, {&off, "-size=false"}, {nil, ""}} {
		args := compileArgs(compileRequest{src: "p.fern", target: "x86-64-linux", size: c.size}, "prog", "stdlib")
		got := ""
		for _, a := range args {
			if strings.HasPrefix(a, "-size") {
				got = a
			}
		}
		if got != c.want {
			t.Errorf("size %v: forwards %q, want %q (%s)", c.size, got, c.want, strings.Join(args, " "))
		}
	}
}
