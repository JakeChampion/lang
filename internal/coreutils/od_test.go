package coreutils

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// odFile writes one input the corpus names and returns its path.
func odFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// odFloats packs `vals` at `size` bytes each, little-endian, so a
// `-t f4` / `-t f8` case dumps values whose printed form is decided by
// gnulib's shortest-round-trip rule rather than by a fixed precision.
func odFloats(vals []float64, size int) []byte {
	out := make([]byte, 0, len(vals)*size)
	for _, v := range vals {
		var b [8]byte
		if size == 4 {
			binary.LittleEndian.PutUint32(b[:4], math.Float32bits(float32(v)))
			out = append(out, b[:4]...)
			continue
		}
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
		out = append(out, b[:]...)
	}
	return out
}

// odExtended packs 80-bit x87 encodings, each in the sixteen bytes the
// ABI gives `long double`: the 64-bit significand with its integer bit
// STORED, then the sign and the biased exponent.
//
// The list is every class the format has except one: a pseudo-denormal
// (a zero exponent with the integer bit set) is left out, because what
// GNU prints for one is an x87 artefact rather than a value — see
// docs/COREUTILS.md's known divergences.
func odExtended() []byte {
	type pat struct {
		lo  uint64
		exp uint16
	}
	pats := []pat{
		{0, 0},                       // zero
		{0, 0x8000},                  // negative zero
		{1, 0},                       // the least subnormal
		{1 << 62, 0},                 // a subnormal at half the least normal
		{0x7fffffffffffffff, 0},      // the greatest subnormal
		{1 << 63, 0x3fff},            // one
		{1 << 63, 0xbfff},            // minus one
		{1 << 63, 0x4000},            // two
		{0xc000000000000000, 0x4000}, // three
		{0x8000000000000001, 0x3fff}, // one plus an ulp
		{1 << 63, 1},                 // the least normal
		{0xffffffffffffffff, 0x7ffe}, // the greatest finite
		{1 << 63, 0x7fff},            // infinity
		{1 << 63, 0xffff},            // negative infinity
		{0xc000000000000000, 0x7fff}, // a quiet NaN
		{0xa000000000000000, 0xffff}, // a negative NaN
		{1 << 62, 0x3fff},            // an unnormal, which is not a value at all
		{0, 0x7fff},                  // a pseudo-infinity, likewise
		{1, 0x7fff},                  // a pseudo-NaN, likewise
		{0x8000000000000000, 0x0002}, // two ulps above the least normal
		{0x9a209a84fbcff798, 0x400c}, // pi times a power of two
		{0xb17217f7d1cf79ab, 0x3ffe}, // ln 2
	}
	out := make([]byte, 0, len(pats)*16)
	for _, p := range pats {
		var b [16]byte
		binary.LittleEndian.PutUint64(b[:8], p.lo)
		binary.LittleEndian.PutUint16(b[8:10], p.exp)
		out = append(out, b[:]...)
	}
	return out
}

// odCases is od(1)'s corpus.
//
// Three things carry most of it. The first is the column arithmetic:
// several `-t` specs on one dump have to end in the same column, so each
// one's block is padded to the widest and the padding is spread over its
// fields — the `-t x1c` and `-t f4 -t f8 -t fL` cases are what pin that,
// and every `z` case pins where the padding stops and the printable
// trailer starts. The second is the shortest-round-trip float rendering,
// which is why the float inputs are built from bit patterns rather than
// from text. The third is the option surface: `-S` requires its value in
// the short spelling and takes one only when glued in the long, `-w`
// takes one only when glued in both, the traditional offset operand is
// read only when no modern option was given, and ten hidden format
// letters GNU never documents still work.
func odCases(t *testing.T) []invocation {
	dir := t.TempDir()
	hello := odFile(t, dir, "hello", []byte("Hello, World!\nSecond line here\n"))
	// Every byte, so -a, -c and the printable trailer all meet their
	// whole table, and a 256-byte file is a whole number of blocks in
	// every width the corpus asks for.
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	allBytes := odFile(t, dir, "allbytes", all)
	// Runs of identical blocks in the middle and at both ends, so the
	// `*` elision is exercised where it starts, where it stops, and
	// against the short last block it may not stand for.
	dup := odFile(t, dir, "dup", []byte(strings.Repeat("A", 16)+strings.Repeat("B", 48)+
		strings.Repeat("C", 16)+strings.Repeat("B", 32)+"DDD"))
	zeros := odFile(t, dir, "zeros", make([]byte, 64))
	empty := odFile(t, dir, "empty", nil)
	short := odFile(t, dir, "short", []byte("abcdefghijklmno"))
	// Printable runs, some NUL-terminated and some not, for -S.
	strs := odFile(t, dir, "strs", []byte("ABC\x00defgh\x00ij\x00ab\tcd\x01ef\x00gh\x7f\xffij\x00"))
	rnd := rand.New(rand.NewSource(8314))
	noise := make([]byte, 300)
	for i := range noise {
		noise[i] = byte(rnd.Intn(256))
	}
	random := odFile(t, dir, "random", noise)
	floatVals := []float64{
		0, math.Copysign(0, -1), 1, -1, 0.5, 1.0 / 3, 1e30, 1e-30,
		3.4028235e38, 1.1754944e-38, 5e-324, math.Inf(1), math.Inf(-1),
		math.NaN(), 123456789, 0.1, 1.5, 7, 255, 65535, 1e-300, -1e-300,
	}
	f4 := odFile(t, dir, "f4", odFloats(floatVals, 4))
	f8 := odFile(t, dir, "f8", odFloats(floatVals, 8))
	fx := odFile(t, dir, "fx", odExtended())
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	raw := odFile(t, dir, "na\xffme", []byte("x\n"))
	// Past a read block, so the elision and the offsets cross one.
	big := odFile(t, dir, "big", []byte(strings.Repeat("0123456789abcdef", 20000)))

	cases := []invocation{
		// The default dump.
		{name: "default", args: []string{hello}},
		{name: "no operand reads stdin", stdin: "hello from stdin\n"},
		{name: "lone dash is stdin", args: []string{"-"}, stdin: "p\nq\n"},
		{name: "stdin twice", args: []string{"-", "-"}, stdin: "abcdef"},
		{name: "stdin and a file", args: []string{"-", hello}, stdin: "abc"},
		{name: "empty stdin", stdin: ""},
		{name: "empty file", args: []string{empty}},
		{name: "two files", args: []string{hello, hello}},
		{name: "a short file", args: []string{short}},
		{name: "every byte", args: []string{allBytes}},
		{name: "past a read block", args: []string{big}},

		// The traditional format letters, including the ten GNU hides.
		{name: "a", args: []string{"-a", allBytes}},
		{name: "b", args: []string{"-b", allBytes}},
		{name: "c", args: []string{"-c", allBytes}},
		{name: "d", args: []string{"-d", allBytes}},
		{name: "f", args: []string{"-f", allBytes}},
		{name: "i", args: []string{"-i", allBytes}},
		{name: "l", args: []string{"-l", allBytes}},
		{name: "o", args: []string{"-o", allBytes}},
		{name: "s", args: []string{"-s", allBytes}},
		{name: "x", args: []string{"-x", allBytes}},
		{name: "hidden B", args: []string{"-B", allBytes}},
		{name: "hidden D", args: []string{"-D", allBytes}},
		{name: "hidden e", args: []string{"-e", allBytes}},
		{name: "hidden F", args: []string{"-F", allBytes}},
		{name: "hidden H", args: []string{"-H", allBytes}},
		{name: "hidden I", args: []string{"-I", allBytes}},
		{name: "hidden L", args: []string{"-L", allBytes}},
		{name: "hidden O", args: []string{"-O", allBytes}},
		{name: "hidden X", args: []string{"-X", allBytes}},
		{name: "hidden h", args: []string{"-h", allBytes}},
		{name: "letters accumulate", args: []string{"-bcdx", allBytes}},
		{name: "every letter", args: []string{"-abcdfilosx", allBytes}},
		{name: "a letter twice", args: []string{"-x", "-x", allBytes}},

		// -t: kinds, sizes and the z trailer.
		{name: "type a", args: []string{"-t", "a", allBytes}},
		{name: "type c", args: []string{"-t", "c", allBytes}},
		{name: "type d default size", args: []string{"-t", "d", allBytes}},
		{name: "type o default size", args: []string{"-t", "o", allBytes}},
		{name: "type u default size", args: []string{"-t", "u", allBytes}},
		{name: "type x default size", args: []string{"-t", "x", allBytes}},
		{name: "type f default size", args: []string{"-t", "f", allBytes}},
		{name: "d1", args: []string{"-t", "d1", allBytes}},
		{name: "d2", args: []string{"-t", "d2", allBytes}},
		{name: "d4", args: []string{"-t", "d4", allBytes}},
		{name: "d8", args: []string{"-t", "d8", allBytes}},
		{name: "o1", args: []string{"-t", "o1", allBytes}},
		{name: "o2", args: []string{"-t", "o2", allBytes}},
		{name: "o4", args: []string{"-t", "o4", allBytes}},
		{name: "o8", args: []string{"-t", "o8", allBytes}},
		{name: "u1", args: []string{"-t", "u1", allBytes}},
		{name: "u2", args: []string{"-t", "u2", allBytes}},
		{name: "u4", args: []string{"-t", "u4", allBytes}},
		{name: "u8", args: []string{"-t", "u8", allBytes}},
		{name: "x1", args: []string{"-t", "x1", allBytes}},
		{name: "x2", args: []string{"-t", "x2", allBytes}},
		{name: "x4", args: []string{"-t", "x4", allBytes}},
		{name: "x8", args: []string{"-t", "x8", allBytes}},
		{name: "size letter C", args: []string{"-t", "xC", allBytes}},
		{name: "size letter S", args: []string{"-t", "xS", allBytes}},
		{name: "size letter I", args: []string{"-t", "xI", allBytes}},
		{name: "size letter L", args: []string{"-t", "xL", allBytes}},
		{name: "size letter C signed", args: []string{"-t", "dC", allBytes}},
		{name: "size letter F", args: []string{"-t", "fF", f4}},
		{name: "size letter D", args: []string{"-t", "fD", f8}},
		{name: "size letter L float", args: []string{"-t", "fL", fx}},
		{name: "two specs in one type string", args: []string{"-t", "x1c", short}},
		{name: "two specs the other way", args: []string{"-t", "cx1", short}},
		{name: "two sizes in one type string", args: []string{"-t", "x1x2", short}},
		{name: "two -t options", args: []string{"-t", "x1", "-t", "x2", short}},
		{name: "six specs", args: []string{"-t", "c", "-t", "a", "-t", "x1", "-t", "o1", "-t", "d1", "-t", "u1", allBytes}},
		{name: "an empty type string", args: []string{"-t", "", short}},
		{name: "z trailer", args: []string{"-t", "x1z", short}},
		{name: "z trailer on a full block", args: []string{"-t", "x1z", allBytes}},
		{name: "z trailer on a named type", args: []string{"-t", "az", short}},
		{name: "z on both specs", args: []string{"-t", "czx1z", short}},
		{name: "z on the second spec", args: []string{"-t", "x1", "-t", "cz", short}},
		{name: "z on the first spec", args: []string{"-t", "cz", "-t", "x1", short}},
		{name: "z on every kind", args: []string{"-t", "az", "-t", "cz", "-t", "x1z", "-t", "d2z", "-t", "f4z", random}},

		// -t faults.
		{name: "a takes no size", args: []string{"-t", "a1", allBytes}},
		{name: "c takes no size", args: []string{"-t", "c1", allBytes}},
		{name: "three-byte integer", args: []string{"-t", "x3", allBytes}},
		{name: "sixteen-byte integer", args: []string{"-t", "x16", allBytes}},
		{name: "thirty-two-byte integer", args: []string{"-t", "x32", allBytes}},
		{name: "sixteen-byte signed", args: []string{"-t", "d16", allBytes}},
		{name: "two-byte float", args: []string{"-t", "f2", allBytes}},
		{name: "ten-byte float", args: []string{"-t", "f10", allBytes}},
		{name: "sixteen-byte float", args: []string{"-t", "f16", fx}},
		{name: "unknown type letter", args: []string{"-t", "q", allBytes}},
		{name: "a comma is not a separator", args: []string{"-t", "x1,x2", allBytes}},
		{name: "a newline in a type string", args: []string{"-t", "x\n", allBytes}},

		// Floating point, where the digits are the shortest round trip.
		{name: "floats", args: []string{"-t", "f4", f4}},
		{name: "floats one per line", args: []string{"-A", "n", "-t", "f4", "-w4", "-v", f4}},
		{name: "doubles", args: []string{"-t", "f8", f8}},
		{name: "doubles one per line", args: []string{"-A", "n", "-t", "f8", "-w8", "-v", f8}},
		{name: "long doubles", args: []string{"-t", "fL", fx}},
		{name: "long doubles one per line", args: []string{"-A", "n", "-t", "fL", "-w16", "-v", fx}},
		{name: "floats over arbitrary bytes", args: []string{"-t", "f4", random}},
		{name: "doubles over arbitrary bytes", args: []string{"-t", "f8", random}},
		{name: "long doubles over arbitrary bytes", args: []string{"-t", "fL", random}},
		{name: "three float widths at once", args: []string{"-t", "f4", "-t", "f8", "-t", "fL", fx}},
		{name: "a float beside its bytes", args: []string{"-t", "f4", "-t", "x4", f4}},

		// -A.
		{name: "address decimal", args: []string{"-A", "d", "-c", hello}},
		{name: "address hex", args: []string{"-A", "x", "-c", hello}},
		{name: "address octal", args: []string{"-A", "o", "-c", hello}},
		{name: "address none", args: []string{"-A", "n", "-c", hello}},
		{name: "address glued", args: []string{"-Ax", hello}},
		{name: "address long", args: []string{"--address-radix=d", hello}},
		{name: "only the first character counts", args: []string{"-A", "dx", hello}},
		{name: "unknown radix", args: []string{"-A", "q", hello}},
		{name: "empty radix", args: []string{"-A", "", hello}},
		{name: "address none with several specs", args: []string{"-A", "n", "-t", "x1", "-t", "c", short}},

		// -j, -N and their counts.
		{name: "skip", args: []string{"-j", "4", "-t", "x1", short}},
		{name: "skip glued", args: []string{"-j4", "-t", "x1", short}},
		{name: "skip to the end", args: []string{"-j", "15", "-t", "x1", short}},
		{name: "skip past the end", args: []string{"-j", "100", "-t", "x1", short}},
		{name: "skip past an empty file", args: []string{"-j", "4", "-t", "x1", empty}},
		{name: "skip across files", args: []string{"-j", "20", "-t", "x1", short, short}},
		{name: "skip past stdin", args: []string{"-j", "20", "-t", "x1"}, stdin: "abc"},
		{name: "skip a pipe", args: []string{"-j", "2", "-t", "x1"}, stdin: "abcdef"},
		{name: "read bytes", args: []string{"-N", "4", "-t", "x1", short}},
		{name: "read no bytes", args: []string{"-N", "0", "-t", "x1", short}},
		{name: "read more than there is", args: []string{"-N", "100", "-t", "x1", short}},
		{name: "skip and read", args: []string{"-j", "2", "-N", "3", "-t", "x1", short}},
		{name: "skip and read a whole block", args: []string{"-j", "16", "-N", "32", "-t", "x1", dup}},
		{name: "hex count", args: []string{"-j", "0x10", "-N", "0x10", "-t", "x1", allBytes}},
		{name: "block suffix", args: []string{"-N", "1b", "-t", "x1", allBytes}},
		{name: "K suffix", args: []string{"-N", "1K", "-t", "x1", allBytes}},
		{name: "kB suffix", args: []string{"-N", "1kB", "-t", "x1", allBytes}},
		{name: "KiB suffix", args: []string{"-N", "1KiB", "-t", "x1", allBytes}},
		{name: "a bare suffix is one of it", args: []string{"-N", "b", "-t", "x1", allBytes}},
		{name: "a bare suffix skipping", args: []string{"-j", "b", "-t", "x1", allBytes}},
		{name: "B is not a bare suffix", args: []string{"-j", "B", "-t", "x1", allBytes}},
		{name: "skip is not a number", args: []string{"-j", "x", "-t", "x1", allBytes}},
		{name: "read is not a number", args: []string{"-N", "x", "-t", "x1", allBytes}},
		{name: "skip has a bad suffix", args: []string{"-j", "1x", "-t", "x1", allBytes}},
		{name: "read has a bad suffix", args: []string{"-N", "1x", "-t", "x1", allBytes}},
		{name: "skip is empty", args: []string{"-j", "", "-t", "x1", allBytes}},
		{name: "read is empty", args: []string{"-N", "", "-t", "x1", allBytes}},
		{name: "skip is too large", args: []string{"-j", "99999999999999999999999", "-t", "x1", allBytes}},
		{name: "read is too large", args: []string{"-N", "99999999999999999999999", "-t", "x1", allBytes}},
		{name: "skip long", args: []string{"--skip-bytes=4", "-t", "x1", allBytes}},
		{name: "read long", args: []string{"--read-bytes=4", "-t", "x1", allBytes}},
		{name: "skip long is not a number", args: []string{"--skip-bytes=1x", "-t", "x1", allBytes}},
		{name: "read long is not a number", args: []string{"--read-bytes=x", "-t", "x1", allBytes}},
		{name: "read long abbreviated", args: []string{"--r", "4", "-t", "x1", allBytes}},

		// -w.
		{name: "width one", args: []string{"-w1", "-t", "x1", short}},
		{name: "width three", args: []string{"-w3", "-t", "x1", short}},
		{name: "width four", args: []string{"-w4", "-t", "x1", short}},
		{name: "width thirty-two", args: []string{"-w32", "-t", "x1", allBytes}},
		{name: "bare width is thirty-two", args: []string{"-w", "-t", "x1", allBytes}},
		{name: "width takes no separate argument", args: []string{"-t", "x1", "-w", "4", short}},
		{name: "width not a multiple of the unit", args: []string{"-w7", "-t", "x2", short}},
		{name: "width long", args: []string{"--width=8", "-t", "x1", allBytes}},
		{name: "width long with no value", args: []string{"--width", "-t", "x1", allBytes}},
		{name: "width long empty", args: []string{"--width=", "-t", "x1", allBytes}},
		{name: "width with a bad suffix", args: []string{"-w1x", "-t", "x1", allBytes}},
		{name: "width past what can be held", args: []string{"-w9223372036854775807", "-t", "x1", allBytes}},
		{name: "width one with characters", args: []string{"-w1", "-t", "c", allBytes}},
		{name: "width seventeen", args: []string{"-w17", "-t", "c", allBytes}},
		{name: "width fourteen for two-byte units", args: []string{"-w14", "-t", "x2", allBytes}},

		// -v and the * elision.
		{name: "elision", args: []string{"-t", "x1", dup}},
		{name: "elision of a whole file", args: []string{zeros}},
		{name: "no elision", args: []string{"-v", "-t", "x1", dup}},
		{name: "no elision of a whole file", args: []string{"-v", zeros}},
		{name: "no elision long", args: []string{"--output-duplicates", "-t", "x1", dup}},
		{name: "elision at a narrower width", args: []string{"-t", "x1", "-w8", dup}},
		{name: "elision at a wider width", args: []string{"-t", "x1", "-w64", dup}},
		{name: "elision cut short", args: []string{"-t", "x1", "-N", "20", dup}},
		{name: "elision starting mid file", args: []string{"-t", "x1", "-j", "16", dup}},
		{name: "elision past a read block", args: []string{"-t", "x1", big}},

		// --endian.
		{name: "endian big", args: []string{"--endian=big", "-t", "x2", allBytes}},
		{name: "endian little", args: []string{"--endian=little", "-t", "x2", allBytes}},
		{name: "endian big four bytes", args: []string{"--endian=big", "-t", "x4", allBytes}},
		{name: "endian big eight bytes", args: []string{"--endian=big", "-t", "x8", allBytes}},
		{name: "endian big single bytes", args: []string{"--endian=big", "-t", "x1", allBytes}},
		{name: "endian big characters", args: []string{"--endian=big", "-t", "c", allBytes}},
		{name: "endian big floats", args: []string{"--endian=big", "-t", "f4", "-t", "f8", f4}},
		{name: "endian big at a partial unit", args: []string{"--endian=big", "-t", "x2", "-w4", short}},
		{name: "endian is not one of the two", args: []string{"--endian=x", "-t", "x2", allBytes}},
		{name: "endian abbreviated", args: []string{"--e=big", "-t", "x2", allBytes}},

		// -S.
		{name: "strings", args: []string{"-S", "3", strs}},
		{name: "strings of two", args: []string{"-S", "2", strs}},
		{name: "strings of four", args: []string{"-S", "4", strs}},
		{name: "strings of one", args: []string{"-S", "1", strs}},
		{name: "strings of none", args: []string{"-S", "0", strs}},
		{name: "strings glued", args: []string{"-S3", strs}},
		{name: "strings long", args: []string{"--strings=2", strs}},
		{name: "strings long with no value", args: []string{"--strings", strs}},
		{name: "strings needs a value in the short form", args: []string{"-S", strs}},
		{name: "strings with no address", args: []string{"-S", "3", "-A", "n", strs}},
		{name: "strings in hex addresses", args: []string{"-A", "x", "-S", "2", strs}},
		{name: "strings of every byte", args: []string{"-S", "3", allBytes}},
		{name: "strings of arbitrary bytes", args: []string{"-S", "3", random}},
		{name: "strings skipping", args: []string{"-S", "3", "-j", "4", strs}},
		{name: "strings bounded", args: []string{"-S", "3", "-N", "12", strs}},
		{name: "strings and a type", args: []string{"-S", "3", "-t", "x1", strs}},
		{name: "strings and duplicates", args: []string{"-S", "3", "-v", strs}},

		// The traditional operand forms.
		{name: "offset operand", args: []string{short, "4"}},
		{name: "signed offset operand", args: []string{short, "+4"}},
		{name: "offset operand alone", args: []string{"+4"}, stdin: "abcdefgh"},
		{name: "hex offset operand", args: []string{short, "+0x4"}},
		{name: "block offset operand", args: []string{short, "+4b"}},
		{name: "an offset with a point is a file", args: []string{short, "+4."}},
		{name: "an offset a traditional letter allows", args: []string{"-x", short, "4"}},
		{name: "a modern option refuses the offset", args: []string{"-t", "x1", short, "4"}},
		{name: "an offset is not read from three operands", args: []string{short, short, "+4"}},
		{name: "traditional offset", args: []string{"--traditional", "-t", "x1", short, "4"}},
		{name: "traditional offset and label", args: []string{"--traditional", short, "4", "5"}},
		{name: "traditional label in decimal", args: []string{"--traditional", "-A", "d", short, "4", "5"}},
		{name: "traditional label with no address", args: []string{"--traditional", "-A", "n", short, "4", "5"}},
		{name: "traditional label and two specs", args: []string{"--traditional", "-t", "x1", "-t", "c", short, "4", "5"}},
		{name: "traditional two offsets", args: []string{"--traditional", "4", "5"}, stdin: "abcdefghij"},
		{name: "traditional one offset", args: []string{"--traditional", "+4"}, stdin: "abcdefghij"},
		{name: "traditional one file", args: []string{"--traditional", short}},
		{name: "traditional with nothing", args: []string{"--traditional"}, stdin: "abc"},
		{name: "traditional second operand is not an offset", args: []string{"--traditional", short, short}},
		{name: "traditional third operand is not an offset", args: []string{"--traditional", short, "4", "x"}},
		{name: "traditional four operands", args: []string{"--traditional", "a", "b", "c", "d"}},
		{name: "an eight is not octal", args: []string{"--traditional", short, "4", "8"}},

		// Operands and the option scan.
		{name: "missing file", args: []string{missing}},
		{name: "two missing files", args: []string{missing, missing}},
		{name: "a missing file before a real one", args: []string{"-A", "o", "-t", "x1", missing, short}},
		{name: "a missing file and stdin", args: []string{missing, "-"}, stdin: ""},
		{name: "a directory operand", args: []string{d}},
		{name: "an operand that is not valid UTF-8", args: []string{raw}},
		{name: "invalid option", args: []string{"-Q", hello}},
		{name: "unrecognized long option", args: []string{"--nope", hello}},
		{name: "ambiguous s", args: []string{"--s", hello}},
		{name: "unambiguous t", args: []string{"--t", hello}},
		{name: "unambiguous w", args: []string{"--w", hello}},
		{name: "unambiguous a", args: []string{"--a", hello}},
		{name: "unambiguous o", args: []string{"--o", hello}},
		{name: "unambiguous f", args: []string{"--f", hello}},
		{name: "format wants an argument", args: []string{"--format"}},
		{name: "radix wants an argument", args: []string{"-A"}},
		{name: "skip wants an argument", args: []string{"-j"}},
		{name: "help with a value", args: []string{"--help=x", hello}},
		{name: "version with a value", args: []string{"--vers=x", hello}},
		{name: "double dash", args: []string{"--", hello}},
		{name: "an option after the operand", args: []string{hello, "-t", "x1"}},
	}

	cases = append(cases,
		invocation{name: "posixly correct stops at the operand", args: []string{hello, "-c"}, env: []string{"POSIXLY_CORRECT=1"}},
		invocation{name: "closed stdout", args: []string{hello}, stdout: stdoutClosed},
		invocation{name: "closed stdout with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		invocation{name: "full stdout", args: []string{hello}, stdout: stdoutFull},
	)
	return cases
}

func TestOd(t *testing.T) {
	requireParity(t, "od", odCases(t))
}

func TestOdHelpVersion(t *testing.T) {
	requireHelp(t, "od", []string{"--help"}, 0)
	requireVersion(t, "od", []string{"--version"}, 0)
}
