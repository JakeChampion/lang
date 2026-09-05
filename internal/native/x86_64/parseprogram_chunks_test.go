package x86_64

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// chunkedSrc is a program several chunks long, with every line shape the
// walk has to keep in order: labels on their own line and ahead of an
// instruction, comments, section switches, data directives, and a
// rip-relative load whose fixup end offset the assembler stamps as it goes.
func chunkedSrc(nfuncs int) string {
	var b strings.Builder
	b.WriteString(".text\n.globl _start\n_start:\n")
	for i := 0; i < nfuncs; i++ {
		fmt.Fprintf(&b, "\tcall f%d # call %d\n", i, i)
	}
	b.WriteString("\tmov rax, 60\n\tmov rdi, 0\n\tsyscall\n")
	for i := 0; i < nfuncs; i++ {
		fmt.Fprintf(&b, "f%d:\n\tpush rbp\n\tmov rbp, rsp\n", i)
		fmt.Fprintf(&b, "\tlea rsi, [rip+s%d]\n", i)
		fmt.Fprintf(&b, "\tmov eax, %d\n\tcmp eax, 0\n\tje .Lz%d\n", i, i)
		fmt.Fprintf(&b, "\tadd rax, 1 // one\n.Lz%d: pop rbp\n\tret\n", i)
	}
	b.WriteString(".rodata\n")
	for i := 0; i < nfuncs; i++ {
		fmt.Fprintf(&b, "s%d:\n\t.asciz \"string %d\"\n", i, i)
	}
	b.WriteString(".bss\nbuf:\n\t.skip 64\n")
	return b.String()
}

func TestParseProgramChunkedMatchesSequential(t *testing.T) {
	src := chunkedSrc(3 * parseChunkSize / 10)
	if n := strings.Count(src, "\n"); n < 3*parseChunkSize {
		t.Fatalf("program is %d lines; the test needs several chunks", n)
	}
	seq, err := parseProgram(src, 1)
	if err != nil {
		t.Fatalf("sequential: %v", err)
	}
	par, err := parseProgram(src, 4)
	if err != nil {
		t.Fatalf("chunked: %v", err)
	}
	wantText, wantData, err := seq.BytesProgram(0x400000)
	if err != nil {
		t.Fatalf("sequential layout: %v", err)
	}
	gotText, gotData, err := par.BytesProgram(0x400000)
	if err != nil {
		t.Fatalf("chunked layout: %v", err)
	}
	if !bytes.Equal(gotText, wantText) {
		t.Errorf(".text differs between the chunked and the sequential parse")
	}
	if !bytes.Equal(gotData, wantData) {
		t.Errorf("data differs between the chunked and the sequential parse")
	}
}

// An error names the same line whichever chunk read it, and the first bad
// line in source order wins even when a later chunk's failure is read
// first.
func TestParseProgramChunkedErrorLine(t *testing.T) {
	lines := strings.Split(chunkedSrc(3*parseChunkSize/10), "\n")
	bad := parseChunkSize + 7
	lines[bad] = "\tmov rax, [rip+nonsense+"
	lines[len(lines)-2] = "\tbogus_mnemonic rax"
	src := strings.Join(lines, "\n")
	_, seqErr := parseProgram(src, 1)
	_, parErr := parseProgram(src, 4)
	if seqErr == nil || parErr == nil {
		t.Fatalf("expected both parses to fail: seq=%v par=%v", seqErr, parErr)
	}
	if seqErr.Error() != parErr.Error() {
		t.Fatalf("error differs:\nsequential: %v\nchunked:    %v", seqErr, parErr)
	}
	if want := fmt.Sprintf("line %d:", bad+1); !strings.HasPrefix(parErr.Error(), want) {
		t.Fatalf("error %q does not name line %d", parErr, bad+1)
	}
}
