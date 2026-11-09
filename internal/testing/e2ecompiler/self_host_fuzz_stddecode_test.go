package e2ecompiler

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math/rand"
	"strings"
	"testing"
)

// The targets here feed hostile bytes to the decoders of the standard library
// through tests/fuzz/stdparse.fern. A client decodes what a server sends, so
// the DEFLATE, gzip and zlib targets compare every input with Go's
// compress/flate, compress/gzip and compress/zlib: the two must agree on
// acceptance, and on the length and CRC-32 of what they decode. Go has no zstd
// or Brotli decoder, so those targets also compress each input with Fern and
// decode it back.

// stdDecodeLimit is the output limit of the stdparse decoders.
const stdDecodeLimit = 1 << 20

// goDecoded is what a Go decoder gives for one input: the output when it
// accepts the input, and whether Go cannot judge the input. Go cannot judge it
// when its output passes the limit, or when it refuses the input for a limit
// of its own that the format does not set.
type goDecoded struct {
	out      []byte
	ok       bool
	unjudged bool
}

func goDecode(open func(io.Reader) (io.Reader, error), input []byte) goDecoded {
	r, err := open(bytes.NewReader(input))
	if err != nil {
		return goDecoded{}
	}
	out, err := io.ReadAll(io.LimitReader(r, stdDecodeLimit+1))
	if len(out) > stdDecodeLimit {
		return goDecoded{unjudged: true}
	}
	return goDecoded{out: out, ok: err == nil}
}

func goInflate(input []byte) goDecoded {
	return goDecode(func(r io.Reader) (io.Reader, error) { return flate.NewReader(r), nil }, input)
}

// goGunzip rejects gzip members with reserved flag bits.
// RFC 1952 requires zero reserved bits, but compress/gzip accepts them.
// gzipStringLimit is the size of the buffer in which compress/gzip reads the
// name and the comment of a member header, the terminating zero included.
const gzipStringLimit = 512

// gzipHeaderPastGo reports whether the header of member has a name or a
// comment that compress/gzip refuses for its buffer. RFC 1952 sets no limit.
func gzipHeaderPastGo(member []byte) bool {
	if len(member) < 10 {
		return false
	}
	flags, p := member[3], 10
	if flags&4 != 0 {
		if len(member) < p+2 {
			return false
		}
		p += 2 + int(binary.LittleEndian.Uint16(member[p:]))
	}
	for _, field := range []byte{8, 16} {
		if flags&field == 0 {
			continue
		}
		if p > len(member) {
			return false
		}
		n := bytes.IndexByte(member[p:], 0)
		if n < 0 {
			return false
		}
		if n >= gzipStringLimit {
			return true
		}
		p += n + 1
	}
	return false
}

func goGunzip(input []byte) goDecoded {
	cursor := bytes.NewReader(input)
	var out []byte
	for cursor.Len() > 0 {
		at := len(input) - cursor.Len()
		if cursor.Len() >= 4 && input[at+3]&0xe0 != 0 {
			return goDecoded{}
		}
		if gzipHeaderPastGo(input[at:]) {
			return goDecoded{unjudged: true}
		}
		r, err := gzip.NewReader(cursor)
		if err != nil {
			return goDecoded{}
		}
		// bytes.Reader preserves the next member boundary in this mode.
		r.Multistream(false)
		part, readErr := io.ReadAll(io.LimitReader(r, int64(stdDecodeLimit-len(out)+1)))
		r.Close()
		if len(out)+len(part) > stdDecodeLimit {
			return goDecoded{unjudged: true}
		}
		if readErr != nil {
			return goDecoded{}
		}
		out = append(out, part...)
	}
	return goDecoded{out: out, ok: len(input) > 0}
}

func TestGzipOracleHeaderPolicy(t *testing.T) {
	var encoded bytes.Buffer
	plain := []byte("gzip member with optional header fields")
	if err := writeGzip(&encoded, flate.BestSpeed, plain); err != nil {
		t.Fatal(err)
	}
	valid := encoded.Bytes()
	for _, flag := range []byte{0, 1, 0x20, 0x40, 0x80, 0xe0} {
		for _, second := range []bool{false, true} {
			t.Run(fmt.Sprintf("flags-%02x/second-%v", flag, second), func(t *testing.T) {
				input := append([]byte(nil), valid...)
				input[3] |= flag
				want := plain
				if second {
					input = append(append([]byte(nil), valid...), input...)
					want = bytes.Repeat(plain, 2)
				}
				got := goGunzip(input)
				ok := flag&0xe0 == 0
				if got.ok != ok || got.unjudged || ok && !bytes.Equal(got.out, want) {
					t.Fatalf("gzip oracle: ok=%v unjudged=%v output=%q, want ok=%v output=%q", got.ok, got.unjudged, got.out, ok, want)
				}
			})
		}
	}
	badCRC := append([]byte(nil), valid...)
	badCRC[len(badCRC)-8] ^= 1
	for _, input := range [][]byte{nil, valid[:len(valid)-1], badCRC, append(append([]byte(nil), valid...), 0)} {
		if got := goGunzip(input); got.ok || got.unjudged {
			t.Errorf("gzip oracle accepts malformed input %x", input)
		}
	}
	for _, c := range []struct {
		name, comment int
		unjudged      bool
	}{{511, 0, false}, {512, 0, true}, {2000, 0, true}, {0, 511, false}, {0, 512, true}} {
		var b bytes.Buffer
		gw := gzip.NewWriter(&b)
		gw.Name = strings.Repeat("n", c.name)
		gw.Comment = strings.Repeat("c", c.comment)
		gw.Write(plain)
		gw.Close()
		got := goGunzip(append(append([]byte(nil), valid...), b.Bytes()...))
		if got.unjudged != c.unjudged || !c.unjudged && (!got.ok || !bytes.Equal(got.out, bytes.Repeat(plain, 2))) {
			t.Errorf("a second member with a name of %d bytes and a comment of %d: ok=%v unjudged=%v, want unjudged=%v", c.name, c.comment, got.ok, got.unjudged, c.unjudged)
		}
	}
	var large bytes.Buffer
	if err := writeGzip(&large, flate.BestSpeed, bytes.Repeat([]byte{'a'}, stdDecodeLimit/2+1)); err != nil {
		t.Fatal(err)
	}
	if got := goGunzip(bytes.Repeat(large.Bytes(), 2)); !got.unjudged || got.ok {
		t.Fatal("gzip oracle does not limit the total output across members")
	}
}

func goZlib(input []byte) goDecoded {
	return goDecode(func(r io.Reader) (io.Reader, error) { return zlib.NewReader(r) }, input)
}

// agreeWithGo compares a summary of the form "READER ok LEN crc CRC ..." or
// "READER refused KIND" with what Go decoded from the same input.
func agreeWithGo(reader string, input []byte, summary string, g goDecoded) string {
	fields := strings.Fields(summary)
	switch {
	case g.unjudged:
		return ""
	case len(fields) >= 5 && fields[1] == "ok":
		want := fmt.Sprintf("%d crc %d", len(g.out), crc32.ChecksumIEEE(g.out))
		if got := strings.Join(fields[2:5], " "); !g.ok || got != want {
			return fmt.Sprintf("%s on %d bytes gives %q, Go gives ok=%v %s\ninput: %x", reader, len(input), summary, g.ok, want, input)
		}
	case len(fields) >= 3 && fields[1] == "refused":
		if g.ok {
			return fmt.Sprintf("%s refuses %d bytes as %s, Go decodes them to %d bytes\ninput: %x", reader, len(input), fields[2], len(g.out), input)
		}
	default:
		return fmt.Sprintf("%s gives %q, which is not a summary", reader, summary)
	}
	return ""
}

// agreesWithGo is the check that compares each summary with what decode gives.
func agreesWithGo(decode func([]byte) goDecoded) readerCheck {
	return func(reader string, input []byte, summary string) string {
		return agreeWithGo(reader, input, summary, decode(input))
	}
}

// compressedSeeds gives text, random bytes and runs, written by a Go
// compressor at several levels.
func compressedSeeds(write func(w io.Writer, level int, data []byte) error) [][]byte {
	rng := rand.New(rand.NewSource(1))
	random := make([]byte, 3000)
	rng.Read(random)
	inputs := [][]byte{
		nil,
		[]byte("hello, hello, hello world\n"),
		[]byte(strings.Repeat("the quick brown fox jumps over the lazy dog. ", 200)),
		random,
		bytes.Repeat([]byte{0}, 70000),
	}
	var out [][]byte
	for _, data := range inputs {
		for _, level := range []int{flate.NoCompression, flate.BestSpeed, flate.BestCompression, flate.HuffmanOnly} {
			var b bytes.Buffer
			if err := write(&b, level, data); err == nil {
				out = append(out, b.Bytes())
			}
		}
	}
	return out
}

// damaged gives copies of a stream with the last byte flipped, the stream cut
// in half, and a byte after the end.
func damaged(stream []byte) [][]byte {
	flipped := append([]byte(nil), stream...)
	flipped[len(flipped)-1] ^= 0xff
	return [][]byte{flipped, stream[:len(stream)/2], append(append([]byte(nil), stream...), 0x55)}
}

func writeFlate(w io.Writer, level int, data []byte) error {
	fw, err := flate.NewWriter(w, level)
	if err != nil {
		return err
	}
	fw.Write(data)
	return fw.Close()
}

func writeGzip(w io.Writer, level int, data []byte) error {
	gw, err := gzip.NewWriterLevel(w, level)
	if err != nil {
		return err
	}
	gw.Name = "name.txt"
	gw.Comment = "comment"
	gw.Extra = []byte{'A', 'B', 2, 0, 'x', 'y'}
	gw.Write(data)
	return gw.Close()
}

func writeZlib(w io.Writer, level int, data []byte) error {
	zw, err := zlib.NewWriterLevel(w, level)
	if err != nil {
		return err
	}
	zw.Write(data)
	return zw.Close()
}

func withDamage(seeds [][]byte) [][]byte {
	out := append([][]byte(nil), seeds...)
	for _, s := range seeds {
		if len(s) > 2 {
			out = append(out, damaged(s)...)
		}
	}
	return out
}

// FuzzSelfHostStdInflate feeds hostile bytes to the raw DEFLATE decoder of
// std/deflate and to compress/flate, and requires the two to agree.
func FuzzSelfHostStdInflate(f *testing.F) {
	fuzzReaderTarget(f)
}

func inflateSeeds(testing.TB) [][]byte {
	seeds := withDamage(compressedSeeds(writeFlate))
	seeds = append(seeds,
		[]byte{0x01, 0x05, 0x00, 0xfa, 0xff, 'h', 'e', 'l', 'l', 'o'},
		[]byte{0x01, 0x05, 0x00, 0xfb, 0xff, 'h', 'e', 'l', 'l', 'o'},
		[]byte{0x07},
		[]byte{0xff, 0xff, 0xff},
		[]byte{},
	)
	return seeds
}

// FuzzSelfHostStdGunzip feeds hostile bytes to the gzip decoder of std/deflate
// and to compress/gzip, and requires the two to agree.
func FuzzSelfHostStdGunzip(f *testing.F) {
	fuzzReaderTarget(f)
}

func gunzipSeeds(testing.TB) [][]byte {
	seeds := withDamage(compressedSeeds(writeGzip))
	two := append(append([]byte(nil), seeds[1]...), seeds[1]...)
	seeds = append(seeds, two, []byte{0x1f, 0x8b}, []byte{0x1f, 0x8b, 8, 0xff, 0, 0, 0, 0, 0, 0}, []byte{0x1f, 0x8b, 7})
	for _, flags := range []byte{0x20, 0x40, 0x80, 0xe0} {
		reserved := append([]byte(nil), seeds[1]...)
		reserved[3] |= flags
		seeds = append(seeds, reserved, append(append([]byte(nil), seeds[1]...), reserved...))
	}
	return seeds
}

// FuzzSelfHostStdZlib feeds hostile bytes to the zlib decoder of std/deflate
// and to compress/zlib, and requires the two to agree.
func FuzzSelfHostStdZlib(f *testing.F) {
	fuzzReaderTarget(f)
}

func zlibSeeds(f testing.TB) [][]byte {
	seeds := withDamage(compressedSeeds(writeZlib))
	var dict bytes.Buffer
	zw, err := zlib.NewWriterLevelDict(&dict, zlib.BestCompression, []byte("hello"))
	if err != nil {
		f.Fatal(err)
	}
	zw.Write([]byte("hello hello"))
	zw.Close()
	seeds = append(seeds, dict.Bytes(), []byte{0x78}, []byte{0x78, 0x9c}, []byte{0x08, 0x1d, 0x03, 0x00, 0x00, 0x00, 0x00, 0x01}, []byte{0x7f, 0x9c})
	return seeds
}

func roundTrips(reader string, input []byte, summary string) string {
	if !strings.HasSuffix(summary, " roundtrip ok") {
		return fmt.Sprintf("%s does not give back %d bytes it compressed: %q\ninput: %x", reader, len(input), summary, input)
	}
	return ""
}

// codecSeeds gives plain inputs for the round trip, and the frames of the
// codec's own test.
func codecSeeds(f testing.TB, frames ...string) [][]byte {
	rng := rand.New(rand.NewSource(2))
	random := make([]byte, 2000)
	rng.Read(random)
	seeds := [][]byte{
		[]byte("hello, hello, hello world\n"),
		[]byte(strings.Repeat("abcabcabd", 500)),
		random,
		bytes.Repeat([]byte{0}, 40000),
		{},
	}
	for _, h := range frames {
		frame := hexSeed(f, h)
		seeds = append(seeds, frame)
		seeds = append(seeds, damaged(frame)...)
	}
	return seeds
}

// FuzzSelfHostStdZstd feeds hostile bytes to the zstd decoder of std/zstd, and
// compresses each input with std/zstd and decodes it back.
func FuzzSelfHostStdZstd(f *testing.F) {
	fuzzReaderTarget(f)
}

func zstdSeeds(f testing.TB) [][]byte {
	seeds := codecSeeds(f,
		"28b52ffd201aa500007068656c6c6f2c2020776f726c640a0100e28a11",
		"28b52ffda4400d03005400001061610100fbff39c002036a08618356bc98",
	)
	seeds = append(seeds, []byte{0x28, 0xb5, 0x2f, 0xfd}, []byte{0x50, 0x2a, 0x4d, 0x18, 4, 0, 0, 0, 1, 2, 3, 4})
	return seeds
}

// FuzzSelfHostStdBrotli feeds hostile bytes to the Brotli decoder of
// std/brotli, and compresses each input with std/brotli and decodes it back.
func FuzzSelfHostStdBrotli(f *testing.F) {
	fuzzReaderTarget(f)
}

func brotliSeeds(f testing.TB) [][]byte {
	seeds := codecSeeds(f,
		"8b0c8068656c6c6f2c2068656c6c6f2c2068656c6c6f20776f726c640a03",
		"2c0161626303",
	)
	seeds = append(seeds, []byte{0x3b}, []byte{0x06}, []byte{0xff})
	return seeds
}
