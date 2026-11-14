package e2ecompiler

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// The target here feeds hostile bytes to the ELF64 reader of std/elf through
// tests/fuzz/stdparse.fern. std/user_process loads a program image through
// that reader, so its input is a file that the program did not write. The
// check is a second reader of the same rules, written here in Go: the two
// must give the same summary for each input.

// elfHeaderImage writes an ELF64 header with the given program header offset, entry
// size and count, and then the program headers.
func elfHeaderImage(kind, machine uint16, entry, phoff uint64, phentsize, phnum uint16, headers ...[]byte) []byte {
	b := make([]byte, 64)
	copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(b[16:], kind)
	binary.LittleEndian.PutUint16(b[18:], machine)
	binary.LittleEndian.PutUint64(b[24:], entry)
	binary.LittleEndian.PutUint64(b[32:], phoff)
	binary.LittleEndian.PutUint16(b[54:], phentsize)
	binary.LittleEndian.PutUint16(b[56:], phnum)
	for _, h := range headers {
		b = append(b, h...)
	}
	return b
}

// elfProgramHeader writes one program header.
func elfProgramHeader(kind, flags uint32, offset, address, fileSize, memorySize uint64) []byte {
	b := make([]byte, 56)
	binary.LittleEndian.PutUint32(b[0:], kind)
	binary.LittleEndian.PutUint32(b[4:], flags)
	binary.LittleEndian.PutUint64(b[8:], offset)
	binary.LittleEndian.PutUint64(b[16:], address)
	binary.LittleEndian.PutUint64(b[24:], address)
	binary.LittleEndian.PutUint64(b[32:], fileSize)
	binary.LittleEndian.PutUint64(b[40:], memorySize)
	binary.LittleEndian.PutUint64(b[48:], 0x1000)
	return b
}

func elfSeeds(testing.TB) [][]byte {
	const max = ^uint64(0)
	load := elfProgramHeader(1, 5, 0, 0x400000, 120, 120)
	seeds := [][]byte{
		{},
		[]byte("\x7fELF"),
		elfHeaderImage(2, 62, 0x401000, 64, 56, 0),
		elfHeaderImage(2, 62, 0x401000, 64, 56, 1, load),
		elfHeaderImage(3, 183, 0x1000, 64, 56, 2, load, elfProgramHeader(4, 4, 64, 0, 56, 56)),
		// A segment that ends beyond the image, and one whose end wraps.
		elfHeaderImage(2, 62, 0, 64, 56, 1, elfProgramHeader(1, 5, 0, 0, 121, 121)),
		elfHeaderImage(2, 62, 0, 64, 56, 1, elfProgramHeader(1, 5, 100, 0, max-50, 0)),
		elfHeaderImage(2, 62, 0, 64, 56, 1, elfProgramHeader(1, 5, max, 0, 2, 0)),
		elfHeaderImage(2, 62, 0, 64, 56, 1, elfProgramHeader(1, 6, 1<<31, 0, 1, 1)),
		elfHeaderImage(2, 62, 0, 64, 56, 1, elfProgramHeader(1, 6, 1<<32, 0, 0, 1)),
		elfHeaderImage(2, 62, 0, 64, 56, 1, elfProgramHeader(1, 6, 120, 0, 0, 1<<40)),
		// More bytes in the file than in memory: a fault of a load segment
		// only.
		elfHeaderImage(2, 62, 0, 64, 56, 1, elfProgramHeader(1, 5, 0, 0x400000, 120, 119)),
		elfHeaderImage(2, 62, 0, 64, 56, 1, elfProgramHeader(1, 5, 64, 0x400000, 56, 0)),
		elfHeaderImage(2, 62, 0, 64, 56, 1, elfProgramHeader(4, 4, 0, 0, 120, 0)),
		// Program headers beyond the image: by the offset, by the count, and
		// by an offset that wraps with the size of the table.
		elfHeaderImage(2, 62, 0, 65, 56, 1, load),
		elfHeaderImage(2, 62, 0, 64, 56, 0xffff, load),
		elfHeaderImage(2, 62, 0, max, 56, 1, load),
		elfHeaderImage(2, 62, 0, max-55, 56, 1, load),
		elfHeaderImage(2, 62, 0, 1<<31, 56, 1, load),
		elfHeaderImage(2, 62, 0, 1<<32+64, 56, 1, load),
		// An entry size that is not 56, with headers and with none.
		elfHeaderImage(2, 62, 0, 64, 55, 1, load),
		elfHeaderImage(2, 62, 0, 64, 0, 0),
		elfHeaderImage(2, 62, max, 0, 56, 1),
	}
	// Each fault of the identification.
	for at, v := range map[int]byte{0: 0, 4: 1, 5: 2, 6: 0} {
		b := elfHeaderImage(2, 62, 0, 64, 56, 1, load)
		b[at] = v
		seeds = append(seeds, b)
	}
	seeds = append(seeds, elfHeaderImage(2, 62, 0, 64, 56, 1, load)[:63])
	return seeds
}

// elfSummaryInGo is the summary of tests/fuzz/stdparse.fern for input, from
// the rules of the ELF64 format alone.
func elfSummaryInGo(in []byte) string {
	n := uint64(len(in))
	fault := ""
	switch {
	case n < 64:
		fault = "0"
	case string(in[:4]) != "\x7fELF":
		fault = "0"
	case in[4] != 2:
		fault = "4"
	case in[5] != 1:
		fault = "5"
	case in[6] != 1:
		fault = "6"
	}
	if fault != "" {
		return "elf refused " + fault
	}
	le := binary.LittleEndian
	phoff, size, count := le.Uint64(in[32:]), uint64(le.Uint16(in[54:])), uint64(le.Uint16(in[56:]))
	if size != 56 && count != 0 {
		return "elf refused 54"
	}
	if phoff > n || count*56 > n-phoff {
		return "elf refused 32"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "elf ok %d %d %d %d", le.Uint16(in[16:]), le.Uint16(in[18:]), le.Uint64(in[24:]), count)
	loads := 0
	for k := uint64(0); k < count; k++ {
		h := in[phoff+k*56:]
		kind, offset, fileSize, memorySize := le.Uint32(h), le.Uint64(h[8:]), le.Uint64(h[32:]), le.Uint64(h[40:])
		if kind == 1 {
			loads++
		}
		// A load segment holds no more bytes in the file than in memory.
		if offset > n || fileSize > n-offset || kind == 1 && fileSize > memorySize {
			fmt.Fprintf(&b, " %d:cut", kind)
			continue
		}
		sum := uint32(0)
		for _, c := range in[offset : offset+fileSize] {
			sum = sum*31 + uint32(c)
		}
		fmt.Fprintf(&b, " %d:%d:%d", kind, fileSize, sum)
	}
	fmt.Fprintf(&b, " loads %d", loads)
	return b.String()
}

func elfAgreesWithGo(r readerRun) string {
	if want := elfSummaryInGo(r.input); r.summary != want {
		return fmt.Sprintf("elf reads %d bytes as %q, the rules of the format give %q\ninput: %x", len(r.input), r.summary, want, r.input)
	}
	return ""
}

// FuzzSelfHostStdELF feeds hostile bytes to the ELF64 reader. The reader and
// the rules of the format, as elfSummaryInGo has them, must agree on each
// input: the refusal and its offset, or the header fields and the bytes of
// each segment.
func FuzzSelfHostStdELF(f *testing.F) {
	fuzzReaderTarget(f)
}

// TestSelfHostStdELFSeeds runs each seed through the reader. The fuzz target
// runs them too, and this test gives each one a name in a plain test run.
func TestSelfHostStdELFSeeds(t *testing.T) {
	bin := fuzzStdParse(t)
	for i, in := range elfSeeds(t) {
		if got, want := runStdReader(t, bin, "elf", in), elfSummaryInGo(in); got != want {
			t.Errorf("seed %d: the reader gives %q, want %q\ninput: %x", i, got, want, in)
		}
	}
}
