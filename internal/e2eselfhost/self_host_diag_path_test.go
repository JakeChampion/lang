package e2eselfhost

import (
	"strings"
	"testing"
)

// A diagnostic in the entry module names the file as the command line did,
// `path:line:col: error[…]`, the way native's does (#11407): a checker error
// and a target-capability refusal (E066) alike. An editor or a script reading
// the position needs the file it is in.
func TestSelfHostDiagnosticsNameTheEntryFile(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range []struct{ name, target, src, want string }{
		{"checker", "x86-64-linux", "function main(): i32 {\n    return nosuch(1);\n}\n", "/main.fern:2:12: error[E001]: "},
		{"capability", "wasm32-wasi", "function main(): i32 {\n    return unix_listen(\"/tmp/x\", 4);\n}\n", "/main.fern:2:23: error[E066]: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// tryEmit writes the program to <dir>/main.fern and names that
			// absolute path on the command line.
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
