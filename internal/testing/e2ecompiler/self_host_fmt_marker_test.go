package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	goparser "github.com/jakechampion/lang/internal/syntax/parser"
	goprinter "github.com/jakechampion/lang/internal/syntax/printer"
)

func TestSelfHostFmtWriteDiagnosticMarkers(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	bin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	for _, source := range []string{
		"// The parser uses /*unknown-stmt*/ as a diagnostic marker.\nfunction main(): i32 {return 0;}\n",
		`function main(): i32 { let s = "/*unknown:literal*/"; return s.len(); }`,
	} {
		prog, err := goparser.Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if out, err := runX86_64Bin(runner, bin, "-fmt", "-w", path).CombinedOutput(); err != nil {
				t.Fatalf("valid marker text refused: %v\n%s", err, out)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != goprinter.Format(prog) {
				t.Fatalf("formatting changed authored marker text: %s", got)
			}
		}
	}
	for _, malformed := range []string{
		`function main(): i32 { let =; return 0; }`,
		`function main(): i32 { return`,
		`function main(): i32 { return 0;`,
		`/*unknown:bad*/`,
	} {
		path := filepath.Join(t.TempDir(), "bad.fern")
		if err := os.WriteFile(path, []byte(malformed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := runX86_64Bin(runner, bin, "-fmt", "-w", path).CombinedOutput(); err == nil {
			t.Fatalf("malformed source was overwritten: %s", out)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != malformed {
			t.Fatalf("refused source changed: %q, %v", got, err)
		}
	}
	// A large source that is already formatted comes back unchanged.
	path := filepath.Join(dir, "parser.fern")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := runX86_64Bin(runner, bin, "-fmt", "-w", path).CombinedOutput(); err != nil {
		t.Fatalf("parser source refused: %v\n%s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("already formatted parser source changed: %v", err)
	}
}
