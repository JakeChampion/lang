package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// unclosedBraceCases each end inside a `{` — a block, a declaration, a match,
// a literal — and every block parser closes one it cannot find the `}` of
// without complaint, so before #11315 the self-host accepted all of them.
var unclosedBraceCases = []struct{ name, src string }{
	{"function-body", "function main(): i32 {\n  return 0;\n"},
	{"struct-decl", "struct P { x: i32\n"},
	{"enum-decl", "enum E { A, B\n"},
	{"if-block", "function main(): i32 {\n  if (true) {\n    return 1;\n  }\n  return 0;\n"},
	{"while-block", "function main(): i32 {\n  while (true) {\n    break;\n  \n  return 0;\n}\n"},
	{"match-arms", "function main(): i32 {\n  let x: i32 = 1;\n  match (x) {\n    1 => { return 1; },\n    _ => { return 0; }\n}\n"},
	{"lambda-body", "function main(): i32 {\n  let f: (i32) => i32 = (a: i32): i32 => {\n    return a;\n  return f(0);\n}\n"},
	// The missing `}` is mid-file: function a swallows main, so the input
	// still ends inside a's body.
	{"before-another-function", "function a(): i32 {\n  return 1;\n\nfunction main(): i32 { return 0; }\n"},
}

// p001At reads the position off either compiler's `path:line:col: error[P001]`
// line; the self-host's message opens with the enclosing construct, native's
// with `expected`.
var p001At = regexp.MustCompile(`:(\d+):(\d+): error\[P001\]: .*expected "\}"`)

// TestSelfHostCheckRejectsUnclosedBraceX86_64 is the differential for
// #11315: `-check` refuses a program that ends inside a `{`, with P001 at
// the end of the input, where native reports its `expected "}"`. It is also
// the formatter's half: `-fmt -w` must refuse to write such a file back,
// since what it would write is a repair nobody asked for.
func TestSelfHostCheckRejectsUnclosedBraceX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	native := buildLangBinForInterp(t)
	for _, c := range unclosedBraceCases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(p, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			nout, _ := exec.Command(native, "-check", p).CombinedOutput()
			nm := p001At.FindSubmatch(nout)
			if nm == nil {
				t.Fatalf("native reported no `expected \"}\"` for this program, so it cannot be the oracle:\n%s", nout)
			}
			var errb bytes.Buffer
			cmd := runX86_64Bin(runner, driver, "-check", p)
			cmd.Stderr = &errb
			if err := cmd.Run(); err == nil {
				t.Fatalf("self-host -check accepted a program that ends inside a `{`")
			}
			var sm [][]byte
			for _, line := range bytes.Split(errb.Bytes(), []byte("\n")) {
				if sm = p001At.FindSubmatch(line); sm != nil {
					break
				}
			}
			if sm == nil {
				t.Fatalf("self-host rejected it without the P001 for the missing `}`:\n%s", errb.String())
			}
			if !bytes.Equal(sm[1], nm[1]) || !bytes.Equal(sm[2], nm[2]) {
				t.Errorf("P001 at %s:%s, want native's %s:%s\n%s", sm[1], sm[2], nm[1], nm[2], errb.String())
			}

			before := []byte(c.src)
			w := runX86_64Bin(runner, driver, "-fmt", "-w", p)
			if err := w.Run(); err == nil {
				t.Errorf("-fmt -w accepted a file that ends inside a `{`")
			}
			if after, _ := os.ReadFile(p); !bytes.Equal(after, before) {
				t.Errorf("-fmt -w rewrote the file:\n%s", after)
			}
		})
	}
}
