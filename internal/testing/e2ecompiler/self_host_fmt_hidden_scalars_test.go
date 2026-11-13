package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	goparser "github.com/jakechampion/lang/internal/syntax/parser"
	goprinter "github.com/jakechampion/lang/internal/syntax/printer"
)

// The self-host formatter writes an escape for each scalar that a reader
// cannot see, as the Go printer does. The source holds each such scalar of
// Unicode as raw bytes, and the scalar before and after each range.
func TestSelfHostFmtEscapesHiddenScalars(t *testing.T) {
	cli := buildSelfHostCLI(t)
	hidden := func(r rune) bool {
		return r >= 0x80 && r <= 0x9f || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Zs) && r != ' '
	}
	scalar := func(r rune) bool {
		return r >= 0x80 && r <= unicode.MaxRune && (r < 0xd800 || r > 0xdfff)
	}
	var text strings.Builder
	escapes := 0
	for r := rune(0x80); r <= unicode.MaxRune; r++ {
		if !scalar(r) {
			continue
		}
		near := scalar(r-1) && hidden(r-1) || scalar(r+1) && hidden(r+1)
		if hidden(r) {
			escapes++
		}
		if hidden(r) || near {
			text.WriteRune(r)
		}
	}
	source := fmt.Sprintf("function main(): i32 {\n  let marks = \"%s\";\n  let f = f\"{marks}%s\";\n  return 0;\n}\n", text.String(), string([]rune{0x202e, 0xe9, 0xfeff}))
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := runX86_64Bin(cli.runner, cli.bin, "-fmt", "-w", path).CombinedOutput(); err != nil {
			t.Fatalf("format pass %d: %v\n%s", pass, err, out)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if pass > 0 && string(before) != string(after) {
			t.Fatal("second format changed the source")
		}
	}
	formatted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := goparser.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if want := goprinter.Format(original); string(formatted) != want {
		t.Fatalf("printer mismatch\n--- Go ---\n%s\n--- Fern ---\n%s", want, formatted)
	}
	for _, r := range string(formatted) {
		if hidden(r) {
			t.Fatalf("the formatted source holds U+%04X as raw bytes", r)
		}
	}
	if got := strings.Count(string(formatted), `\u{`); got != escapes+2 {
		t.Fatalf("%d escapes, want %d", got, escapes+2)
	}
	if !strings.Contains(string(formatted), string(rune(0xe9))) || !strings.Contains(string(formatted), `\u{202E}`+string(rune(0xe9))+`\u{FEFF}`) {
		t.Fatalf("a scalar that a reader can see has an escape:\n%s", formatted)
	}
	// The escapes give the scalars of the source back.
	again, err := goparser.Parse(string(formatted))
	if err != nil {
		t.Fatal(err)
	}
	if goprinter.Format(again) != string(formatted) {
		t.Fatal("the formatted source does not parse to the same literals")
	}
}
