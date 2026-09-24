package e2eselfhost

import (
	"path/filepath"
	"strings"
	"testing"
)

// arg_at and args_count are not builtins: native's checker reports each as
// undefined, and the self-host used to compile both (#10183). A program reads
// argv through args().
func TestSelfHostRetiredArgvBuiltinsUndefined(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, src string }{
		{"arg_at", "function main(): i32 {\n  var a: string = arg_at(1);\n  return a.len();\n}\n"},
		{"args_count", "function main(): i32 {\n  return args_count();\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := mustWrite(t, t.TempDir(), "main.fern", tc.src)
			want := `error[E001]: undefined function "` + tc.name + `"`
			for _, mode := range [][]string{
				{"-check", src, stdlibRoot},
				{"-target", "x86-64-linux", "-emit", "asm", src, stdlibRoot, "-o", filepath.Join(t.TempDir(), "out.s")},
				{"-target", "wasm32-wasi", "-emit", "asm", src, stdlibRoot, "-o", filepath.Join(t.TempDir(), "out.wat")},
			} {
				out, err := runX86_64Bin(runner, fernBin, mode...).CombinedOutput()
				if err == nil {
					t.Fatalf("%v: accepted a call to %s", mode[:2], tc.name)
				}
				if !strings.Contains(string(out), want) {
					t.Fatalf("%v: diagnostic %q does not name %s as undefined", mode[:2], out, tc.name)
				}
			}
		})
	}
}
