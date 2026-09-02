package elf_test

import (
	"bytes"
	"debug/dwarf"
	goelf "debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
)

// lineRow is what a DWARF consumer sees of one line-table row, the address
// made relative to the start of .text so an object file and a final image
// compare.
type lineRow struct {
	off           uint64
	file          string
	line, col     int
	isStmt        bool
	prologueEnd   bool
	epilogueBegin bool
}

func decodeLineRows(t *testing.T, d *dwarf.Data, textLo uint64) []lineRow {
	t.Helper()
	cu, err := d.Reader().Next()
	if err != nil || cu == nil {
		t.Fatalf("no CU: %v", err)
	}
	lr, err := d.LineReader(cu)
	if err != nil {
		t.Fatalf("LineReader: %v", err)
	}
	var rows []lineRow
	for {
		var le dwarf.LineEntry
		if err := lr.Next(&le); err != nil {
			break
		}
		if le.EndSequence {
			continue
		}
		rows = append(rows, lineRow{le.Address - textLo, le.File.Name, le.Line, le.Column, le.IsStmt, le.PrologueEnd, le.EpilogueBegin})
	}
	return rows
}

// TestDebugLineMatchesGNUAs decodes the same `.file` / `.loc` stream
// through gas and through this writer and requires the two line tables to
// mean the same thing: the same rows, each with the same file, line, column
// and flags. The bytes are not compared — gas writes DWARF 3 and chooses
// its own opcodes — because what a debugger reads is the decoded table, and
// that is where a wrong dir index, a dropped column, or a misplaced
// prologue_end would show.
func TestDebugLineMatchesGNUAs(t *testing.T) {
	as := findX86As(t)
	src := "\t.intel_syntax noprefix\n\t.text\n" +
		"\t.file 1 \"main.fern\"\n\t.file 2 \"lib/util.fern\"\n\t.file 3 \"/abs/dir/deep.fern\"\n\t.file 4 \"lib/other.fern\"\n" +
		"\t.globl f\nf:\n" +
		"\t.loc 1 3 5\n\tnop\n" +
		"\t.loc 1 3 9 prologue_end\n\tnop\n\tnop\n" +
		"\t.loc 2 10 1\n\tnop\n" +
		"\t.loc 2 12 3 is_stmt 0\n\tnop\n" +
		"\t.loc 2 11 3\n\tnop\n" + // line goes backwards
		"\t.loc 3 200 7\n\tnop\n" + // line delta outside the special-opcode range
		"\t.loc 4 201 7 epilogue_begin\n" + pad(300) + // address advance outside it
		"\t.loc 1 4 1\n\tnop\n" +
		"\t.loc 1 4 1\n\tret\n" // a second row at a new address, same position

	// The object has no comp_dir, so debug/dwarf joins its relative names
	// with the working directory; the image is given the same one.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sPath, oPath := filepath.Join(dir, "l.s"), filepath.Join(dir, "l.o")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(as, "--64", "-o", oPath, sPath).CombinedOutput(); err != nil {
		t.Fatalf("gas: %v\n%s", err, out)
	}
	gf, err := goelf.Open(oPath)
	if err != nil {
		t.Fatal(err)
	}
	defer gf.Close()
	gd, err := gf.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	want := decodeLineRows(t, gd, 0)

	a, err := x86_64.ParseProgram(src)
	if err != nil {
		t.Fatal(err)
	}
	text, data, err := a.BytesProgramWX(elf.TextVAddrWX, elf.TextVAddrWX+0x1000)
	if err != nil {
		t.Fatal(err)
	}
	base := uint64(elf.TextVAddrWX)
	var rows []elf.LineRow
	for _, r := range a.LocRows() {
		rows = append(rows, elf.LineRow{Addr: base + uint64(r.Offset), File: r.File, Line: r.Line, Col: r.Col,
			PrologueEnd: r.PrologueEnd, EpilogueBegin: r.EpilogueBegin, IsStmt: r.IsStmt})
	}
	img, err := elf.StaticExecutableDataX86WXDebug(text, elf.Unwind{}, data, elf.Debug{
		Syms: []elf.Sym{{Name: "f", Value: base, Size: uint64(len(text))}}, Files: a.Files(), Rows: rows,
		SrcFile: "main.fern", CompDir: cwd, TextEnd: base + uint64(len(text)),
	})
	if err != nil {
		t.Fatal(err)
	}
	of, err := goelf.NewFile(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	od, err := of.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	got := decodeLineRows(t, od, base)

	if len(got) != len(want) {
		t.Fatalf("got %d rows, gas produced %d\ngas:  %+v\nours: %+v", len(got), len(want), want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: ours %+v, gas %+v", i, got[i], want[i])
		}
	}
}

func pad(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "\tnop\n"
	}
	return s
}

// findX86As returns a GNU as that assembles x86-64, or skips. The source
// below is x86-64 Intel syntax, so the aarch64 lane's native `as` cannot
// build it — it rejects `--64` outright — and the probe uses the exact
// invocation the test does rather than assuming the name implies the target.
func findX86As(t *testing.T) string {
	t.Helper()
	as, err := exec.LookPath("x86_64-linux-gnu-as")
	if err != nil {
		if as, err = exec.LookPath("as"); err != nil {
			t.Skip("no GNU as on PATH")
		}
	}
	dir := t.TempDir()
	sPath := filepath.Join(dir, "probe.s")
	if err := os.WriteFile(sPath, []byte(".intel_syntax noprefix\n.text\nret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(as, "--64", "-o", filepath.Join(dir, "probe.o"), sPath).Run(); err != nil {
		t.Skipf("%s does not assemble x86-64: %v", as, err)
	}
	return as
}
