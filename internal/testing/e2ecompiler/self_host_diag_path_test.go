package e2ecompiler

import (
	"bytes"
	"strings"
	"testing"
)

func TestSelfHostDiagnosticsNameTheEntryFile(t *testing.T) {
	cli := newStrictCLI(t)
	t.Run("stdin", func(t *testing.T) {
		cmd := runX86_64Bin(cli.runner, cli.bin, "-check", "-", cli.stdlib)
		cmd.Stdin = bytes.NewReader([]byte("function main(): i32 {\n    return nosuch(1);\n}\n"))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			t.Fatalf("-check accepted a program that must be refused; diagnostics:\n%s", stderr.String())
		}
		if first := strings.SplitN(stderr.String(), "\n", 2)[0]; !strings.HasPrefix(first, "<stdin>:2:12: error[E001]: ") {
			t.Errorf("first diagnostic line %q, want it to open with <stdin>:2:12: error[E001]: ", first)
		}
	})
	for _, tc := range []struct{ name, target, src, want string }{
		{"checker", "x86-64-linux", "function main(): i32 {\n    return nosuch(1);\n}\n", "/main.fern:2:12: error[E001]: "},
		{"capability", "wasm32-wasi", "function main(): i32 {\n    return unix_listen(\"/tmp/x\", 4);\n}\n", "/main.fern:2:23: error[E066]: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, diags, err := cli.tryEmit(t, tc.target, tc.src)
			if err == nil {
				t.Fatalf("compile accepted a program that must be refused; diagnostics:\n%s", diags)
			}
			first := strings.SplitN(diags, "\n", 2)[0]
			if !strings.HasPrefix(first, "/") || !strings.Contains(first, tc.want) {
				t.Errorf("first diagnostic line %q, want the entry's absolute path then %q", first, tc.want)
			}
		})
	}
}
