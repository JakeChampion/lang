package e2ecompiler

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The targets here feed hostile bytes to six readers of file formats in the
// standard library through tests/fuzz/stdparse.fern: the cpio archive reader
// of std/cpio, the class file reader of std/classfile, the device tree reader
// of std/fdt, the font reader and rasteriser of std/truetype, the TZif reader
// of std/tz and the CSV reader of std/csv. A program reads these files from a
// disk, a boot loader or a peer. Each summary also reads back what the parse
// gave, because a parse that accepts a bad offset fails at the first read.

// cpioEntry writes one entry of the newc format, or of the format with a
// checksum when magic is 070702.
func cpioEntry(magic, name string, mode uint32, data []byte, check uint32) []byte {
	fields := []uint32{1, mode, 0, 0, 1, 0, uint32(len(data)), 0, 0, 0, 0, uint32(len(name) + 1), check}
	b := []byte(magic)
	for _, f := range fields {
		b = append(b, fmt.Sprintf("%08x", f)...)
	}
	b = append(append(b, name...), 0)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	b = append(b, data...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func cpioArchive(entries ...[]byte) []byte {
	var b []byte
	for _, e := range entries {
		b = append(b, e...)
	}
	return append(b, cpioEntry("070701", "TRAILER!!!", 0, nil, 0)...)
}

func cpioSeeds(testing.TB) [][]byte {
	file := cpioEntry("070701", "etc/motd", 0o100644, []byte("hello\n"), 0)
	huge := cpioEntry("070701", "big", 0o100644, nil, 0)
	copy(huge[6+6*8:], "7fffffff")
	wrap := cpioEntry("070701", "wrap", 0o100644, nil, 0)
	copy(wrap[6+6*8:], "fffffffc")
	name := cpioEntry("070701", "n", 0o100644, nil, 0)
	copy(name[6+11*8:], "ffffffff")
	return [][]byte{
		{},
		cpioArchive(),
		cpioArchive(cpioEntry("070701", "etc", 0o040755, nil, 0), file, cpioEntry("070701", "etc/link", 0o120777, []byte("motd"), 0)),
		cpioArchive(cpioEntry("070702", "sum", 0o100644, []byte("ab"), 'a'+'b')),
		cpioArchive(cpioEntry("070702", "sum", 0o100644, []byte("ab"), 1)),
		cpioArchive(cpioEntry("070701", "../../up", 0o100644, []byte("x"), 0), cpioEntry("070701", "/abs//./x", 0o100644, nil, 0)),
		// No trailer, a size beyond the bytes, a size that wraps with its
		// padding, a name size beyond the bytes, and a field that is not
		// hexadecimal.
		file,
		cpioArchive(huge),
		cpioArchive(wrap),
		cpioArchive(name),
		[]byte(strings.Replace(string(cpioArchive(file)), "00000001", "0000000g", 1)),
	}
}

// fdtBlob writes a flattened device tree of version 17 with the given
// structure block and strings block.
func fdtBlob(structure, names []byte) []byte {
	const header = 40
	b := make([]byte, header+16)
	be := binary.BigEndian
	be.PutUint32(b[0:], 0xd00dfeed)
	be.PutUint32(b[4:], uint32(header+16+len(structure)+len(names)))
	be.PutUint32(b[8:], header+16)
	be.PutUint32(b[12:], uint32(header+16+len(structure)))
	be.PutUint32(b[16:], header)
	be.PutUint32(b[20:], 17)
	be.PutUint32(b[24:], 16)
	be.PutUint32(b[32:], uint32(len(names)))
	be.PutUint32(b[36:], uint32(len(structure)))
	return append(append(b, structure...), names...)
}

func fdtWord(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

func fdtPadded(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func fdtNode(name string, body ...[]byte) []byte {
	b := append(fdtWord(1), fdtPadded(append([]byte(name), 0))...)
	for _, part := range body {
		b = append(b, part...)
	}
	return append(b, fdtWord(2)...)
}

func fdtProp(nameOffset uint32, data []byte) []byte {
	b := append(fdtWord(3), fdtWord(uint32(len(data)))...)
	return append(append(b, fdtWord(nameOffset)...), fdtPadded(append([]byte(nil), data...))...)
}

func fdtSeeds(testing.TB) [][]byte {
	names := []byte("compatible\x00reg\x00#address-cells\x00#size-cells\x00")
	const compatible, reg, addressCells, sizeCells = 0, 11, 15, 30
	cells := func(vs ...uint32) []byte {
		var b []byte
		for _, v := range vs {
			b = append(b, fdtWord(v)...)
		}
		return b
	}
	uart := fdtNode("uart@9000000", fdtProp(compatible, []byte("arm,pl011\x00arm,primecell\x00")), fdtProp(reg, cells(0, 0x9000000, 0, 0x1000)))
	root := fdtNode("", fdtProp(addressCells, cells(2)), fdtProp(sizeCells, cells(2)), uart)
	tree := append(root, fdtWord(9)...)
	deep := fdtWord(9)
	for range 40 {
		deep = append(fdtNode("n", deep[:len(deep)-4]), fdtWord(9)...)
	}
	short := fdtBlob(tree, names)
	binary.BigEndian.PutUint32(short[4:], 16)
	beyond := fdtBlob(tree, names)
	binary.BigEndian.PutUint32(beyond[8:], 0xfffffff0)
	strings := fdtBlob(tree, names)
	binary.BigEndian.PutUint32(strings[12:], 0x7fffffff)
	return [][]byte{
		{},
		fdtBlob(append(fdtNode(""), fdtWord(9)...), nil),
		fdtBlob(tree, names),
		fdtBlob(deep, names),
		// A property whose length is beyond the block, one whose name is
		// beyond the strings, cells of a count that the property does not
		// hold, and a tree with no end.
		fdtBlob(append(fdtNode("", append(fdtWord(3), cells(0xfffffffc, reg)...)), fdtWord(9)...), names),
		fdtBlob(append(fdtNode("", fdtProp(0x7fffffff, cells(1))), fdtWord(9)...), names),
		fdtBlob(append(fdtNode("", fdtProp(addressCells, cells(0xffffffff)), fdtProp(sizeCells, cells(0x40000000)), fdtProp(reg, cells(1, 2, 3))), fdtWord(9)...), names),
		fdtBlob(root, names),
		short, beyond, strings,
	}
}

// classFile writes a class file with the given constant pool entries after
// the count, and the bytes that follow the pool.
func classFile(count uint16, pool, rest []byte) []byte {
	b := []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 52}
	b = binary.BigEndian.AppendUint16(b, count)
	return append(append(b, pool...), rest...)
}

func classUTF8(s string) []byte {
	return append(binary.BigEndian.AppendUint16([]byte{1}, uint16(len(s))), s...)
}

func classSeeds(testing.TB) [][]byte {
	u16 := func(vs ...uint16) []byte {
		var b []byte
		for _, v := range vs {
			b = binary.BigEndian.AppendUint16(b, v)
		}
		return b
	}
	// 1 the class name, 2 its class, 3 Object, 4 its class, 5 a field name,
	// 6 a field descriptor, 7 a method name, 8 a method descriptor,
	// 9 ConstantValue, 10 an int.
	var pool []byte
	pool = append(pool, classUTF8("p/Thing")...)
	pool = append(pool, 7, 0, 1)
	pool = append(pool, classUTF8("java/lang/Object")...)
	pool = append(pool, 7, 0, 3)
	pool = append(pool, classUTF8("LIMIT")...)
	pool = append(pool, classUTF8("I")...)
	pool = append(pool, classUTF8("run")...)
	pool = append(pool, classUTF8("([Ljava/lang/String;J)V")...)
	pool = append(pool, classUTF8("ConstantValue")...)
	pool = append(pool, 3, 0, 0, 0, 42)
	field := append(u16(0x19, 5, 6, 1), append(u16(9), append([]byte{0, 0, 0, 2}, u16(10)...)...)...)
	method := u16(0x9, 7, 8, 0)
	body := append(u16(0x21, 2, 4, 1, 4, 1), field...)
	body = append(append(body, u16(1)...), method...)
	body = append(body, u16(0)...)
	whole := classFile(11, pool, body)
	// A long takes two slots of the pool.
	wide := classFile(4, append([]byte{5, 0, 0, 0, 0, 0, 0, 0, 7}, classUTF8("x")...), u16(0x21, 3, 3, 0, 0, 0, 0))
	seeds := [][]byte{
		{},
		{0xca, 0xfe, 0xba, 0xbe},
		whole,
		wide,
		classFile(0, nil, u16(0x21, 1, 1, 0, 0, 0, 0)),
		classFile(0xffff, pool, body),
		// A class index that is not in the pool, one that names an int, an
		// attribute length beyond the bytes, and counts with no members.
		classFile(11, pool, u16(0x21, 60, 4, 0, 0, 0, 0)),
		classFile(11, pool, u16(0x21, 10, 4, 0, 0, 0, 0)),
		classFile(11, pool, append(u16(0x21, 2, 4, 0, 1, 0x19, 5, 6, 1, 9), 0xff, 0xff, 0xff, 0xf0)),
		classFile(11, pool, u16(0x21, 2, 4, 0xffff, 0xffff, 0xffff)),
		classFile(2, append(binary.BigEndian.AppendUint16([]byte{1}, 0xffff), 'a'), nil),
	}
	for cut := 9; cut < len(whole); cut += 13 {
		seeds = append(seeds, whole[:cut])
	}
	return seeds
}

// tzifBlock writes one data block of a TZif file: transitions at the given
// times, two local time types, and the names.
func tzifBlock(version byte, width int, times []int64, types []byte, names string, counts ...uint32) []byte {
	b := make([]byte, 44)
	copy(b, "TZif")
	b[4] = version
	header := []uint32{0, 0, 0, uint32(len(times)), 2, uint32(len(names))}
	copy(header, counts)
	for i, v := range header {
		binary.BigEndian.PutUint32(b[20+4*i:], v)
	}
	for _, at := range times {
		if width == 4 {
			b = binary.BigEndian.AppendUint32(b, uint32(at))
		} else {
			b = binary.BigEndian.AppendUint64(b, uint64(at))
		}
	}
	b = append(b, types...)
	b = binary.BigEndian.AppendUint32(b, 3600)
	b = append(b, 0, 0)
	b = binary.BigEndian.AppendUint32(b, 7200)
	b = append(b, 1, 4)
	return append(b, names...)
}

func tzifSeeds(testing.TB) [][]byte {
	names := "CET\x00CEST\x00"
	times := []int64{1000, 2000, 1 << 40}
	v1 := tzifBlock(0, 4, times[:2], []byte{1, 0}, names)
	v2 := func(footer string, types []byte, counts ...uint32) []byte {
		b := append(v1, tzifBlock('2', 8, times, types, names, counts...)...)
		return append(b, footer...)
	}
	seeds := [][]byte{
		{},
		[]byte("TZif"),
		v1,
		v2("\nCET-1CEST,M3.5.0,M10.5.0/3\n", []byte{1, 0, 1}),
		v2("\n<+0330>-3:30\n", []byte{1, 0, 1}),
		v2("", []byte{1, 0, 1}),
		// A type index and a name index that are not in their tables, times
		// that are not in order, a rule with no end, and counts beyond the
		// bytes.
		v2("\nCET-1\n", []byte{1, 9, 255}),
		tzifBlock(0, 4, []int64{2000, 1000}, []byte{0, 1}, "C"),
		v2("\nCET-1CEST,M13.9.9/99999999999,J0\n", []byte{1, 0, 1}),
		v2("\n"+strings.Repeat("<", 600), []byte{1, 0, 1}),
		tzifBlock(0, 4, nil, nil, names, 0xffffffff, 0xffffffff, 0xffffffff, 0xffffffff, 0xffffffff, 0xffffffff),
		tzifBlock(0, 4, times[:2], []byte{1, 0}, names, 0, 0, 0, 0x7fffffff),
		tzifBlock(0, 4, times[:2], []byte{1, 0}, names, 0, 0, 1<<28),
	}
	// A zone of the host, when it has one below the input limit.
	for _, zone := range []string{"Europe/London", "America/New_York", "Asia/Kolkata"} {
		if b, err := os.ReadFile("/usr/share/zoneinfo/" + zone); err == nil && len(b) <= fuzzInputLimit {
			seeds = append(seeds, b)
		}
	}
	return seeds
}

// fontSeedFiles are fonts of the tree. Each is above the input limit of a
// fuzz run, so the seeds are their first bytes and their table directories.
var fontSeedFiles = [][]string{
	{"examples", "android", "paper", "testfonts", "Roboto-Regular.ttf"},
	{"site", "src", "assets", "fonts", "card", "ibm-plex-mono-500.ttf"},
}

func readFont(tb testing.TB, parts []string) []byte {
	b, err := os.ReadFile(e2eharness.RepoPath(parts...))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func truetypeSeeds(tb testing.TB) [][]byte {
	seeds := [][]byte{{}, {0, 1, 0, 0}, {0, 1, 0, 0, 0xff, 0xff, 0, 0, 0, 0, 0, 0}}
	for _, parts := range fontSeedFiles {
		font := readFont(tb, parts)
		tables := int(binary.BigEndian.Uint16(font[4:]))
		directory := 12 + 16*tables
		seeds = append(seeds, font[:directory], font[:fuzzInputLimit], font[:fuzzInputLimit/4])
		// Each table at offset 0, at the end, and beyond the end of the
		// first bytes of the font.
		for _, offset := range []uint32{0, uint32(fuzzInputLimit/4 - 4), 0xfffffff0} {
			moved := append([]byte(nil), font[:fuzzInputLimit/4]...)
			for k := 0; k < tables; k++ {
				binary.BigEndian.PutUint32(moved[12+16*k+8:], offset)
			}
			seeds = append(seeds, moved)
		}
	}
	return seeds
}

func csvSeeds(testing.TB) [][]byte {
	var seeds [][]byte
	for _, s := range []string{
		"", "a,b,c\n", "a,b\r\nc,d\r\n", "\"q,1\",\"two\nlines\",\"a \"\"b\"\"\"\n", ",,\n\n,\n",
		"\"open", "a\"b", "\"a\"b", "a\rb", "\xff,\xc3\n", "é,日本\n", strings.Repeat("a,", 2000), strings.Repeat("\"\"", 999),
	} {
		seeds = append(seeds, []byte(s))
	}
	return seeds
}

// csvRoundTrips requires that a document the reader accepts gives the same
// rows after the writer and a second read.
func csvRoundTrips(r readerRun) string {
	if strings.HasPrefix(r.summary, "csv ok ") && !strings.HasSuffix(r.summary, " same") {
		return fmt.Sprintf("csv reads %d bytes, and the rows change after serialize and a second parse: %s\ninput: %q", len(r.input), r.summary, r.input)
	}
	return ""
}

func FuzzSelfHostStdCpio(f *testing.F) {
	fuzzReaderTarget(f)
}

func FuzzSelfHostStdClassFile(f *testing.F) {
	fuzzReaderTarget(f)
}

func FuzzSelfHostStdFdt(f *testing.F) {
	fuzzReaderTarget(f)
}

func FuzzSelfHostStdTrueType(f *testing.F) {
	fuzzReaderTarget(f)
}

func FuzzSelfHostStdTZif(f *testing.F) {
	fuzzReaderTarget(f)
}

func FuzzSelfHostStdCSV(f *testing.F) {
	fuzzReaderTarget(f)
}

// TestSelfHostStdFileReaderSeeds runs each seed of the six readers, and each
// whole font of the tree, in a plain test run. A seed must give a summary
// with exit 0 inside the budgets of a fuzz run.
func TestSelfHostStdFileReaderSeeds(t *testing.T) {
	bin := fuzzStdParse(t)
	for _, target := range []string{"FuzzSelfHostStdCpio", "FuzzSelfHostStdClassFile", "FuzzSelfHostStdFdt", "FuzzSelfHostStdTrueType", "FuzzSelfHostStdTZif", "FuzzSelfHostStdCSV"} {
		r := readerTargetOf(t, target)
		t.Run(r.reader, func(t *testing.T) {
			accepted := 0
			for i, in := range r.seeds(t) {
				summary := runStdReader(t, bin, r.reader, in)
				if !strings.HasPrefix(summary, r.reader+" ") {
					t.Errorf("seed %d: the summary is %q", i, summary)
				}
				if strings.HasPrefix(summary, r.reader+" ok") {
					accepted++
				}
				if r.check != nil {
					if fault := r.check(readerRun{reader: r.reader, input: in, summary: summary}); fault != "" {
						t.Errorf("seed %d: %s", i, fault)
					}
				}
			}
			if accepted == 0 {
				t.Errorf("the reader accepts no seed, so no seed reaches the reads behind the parse")
			}
		})
	}
	for _, parts := range fontSeedFiles {
		if got := runStdReader(t, bin, "truetype", readFont(t, parts)); !strings.HasPrefix(got, "truetype ok ") {
			t.Errorf("%s: the summary is %q", parts[len(parts)-1], got)
		}
	}
}
