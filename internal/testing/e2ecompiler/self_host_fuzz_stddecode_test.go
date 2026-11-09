package e2ecompiler

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"hash/crc32"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
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

// fuzzStdOracle is fuzzStdParser with a check on the summary of each input.
func fuzzStdOracle(f *testing.F, reader string, seeds [][]byte, check func(t *testing.T, input []byte, summary string)) {
	bin := fuzzStdParse(f)
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > fuzzInputLimit {
			t.Skip("over the input limit")
		}
		path := filepath.Join(t.TempDir(), "input")
		if err := os.WriteFile(path, input, 0o644); err != nil {
			t.Fatal(err)
		}
		r := runUnderBudget(exec.Command(bin, reader, path))
		if failOutsideBudget(t, "stdparse "+reader, r, input) {
			return
		}
		if r.code != 0 || !strings.HasPrefix(r.stdout, reader+" ") {
			t.Fatalf("exit %d on %d bytes, want 0 with a %s summary\nstdout: %q\nstderr: %q", r.code, len(input), reader, r.stdout, r.stderr)
		}
		check(t, input, strings.TrimSpace(r.stdout))
	})
}

// goDecoded is what a Go decoder gives for one input: the output when it
// accepts the input, and whether the output passed the limit.
type goDecoded struct {
	out  []byte
	ok   bool
	over bool
}

func goDecode(open func(io.Reader) (io.Reader, error), input []byte) goDecoded {
	r, err := open(bytes.NewReader(input))
	if err != nil {
		return goDecoded{}
	}
	out, err := io.ReadAll(io.LimitReader(r, stdDecodeLimit+1))
	if len(out) > stdDecodeLimit {
		return goDecoded{over: true}
	}
	return goDecoded{out: out, ok: err == nil}
}

func goInflate(input []byte) goDecoded {
	return goDecode(func(r io.Reader) (io.Reader, error) { return flate.NewReader(r), nil }, input)
}

func goGunzip(input []byte) goDecoded {
	return goDecode(func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) }, input)
}

func goZlib(input []byte) goDecoded {
	return goDecode(func(r io.Reader) (io.Reader, error) { return zlib.NewReader(r) }, input)
}

// agreeWithGo compares a summary of the form "READER ok LEN crc CRC ..." or
// "READER refused KIND" with what Go decoded from the same input.
func agreeWithGo(t *testing.T, reader string, input []byte, summary string, g goDecoded) {
	t.Helper()
	fields := strings.Fields(summary)
	switch {
	case g.over:
		return
	case len(fields) >= 5 && fields[1] == "ok":
		want := fmt.Sprintf("%d crc %d", len(g.out), crc32.ChecksumIEEE(g.out))
		if got := strings.Join(fields[2:5], " "); !g.ok || got != want {
			t.Errorf("%s on %d bytes gives %q, Go gives ok=%v %s\ninput: %x", reader, len(input), summary, g.ok, want, input)
		}
	case len(fields) >= 3 && fields[1] == "refused":
		if g.ok {
			t.Errorf("%s refuses %d bytes as %s, Go decodes them to %d bytes\ninput: %x", reader, len(input), fields[2], len(g.out), input)
		}
	default:
		t.Errorf("%s gives %q, which is not a summary", reader, summary)
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
	seeds := withDamage(compressedSeeds(writeFlate))
	seeds = append(seeds,
		[]byte{0x01, 0x05, 0x00, 0xfa, 0xff, 'h', 'e', 'l', 'l', 'o'},
		[]byte{0x01, 0x05, 0x00, 0xfb, 0xff, 'h', 'e', 'l', 'l', 'o'},
		[]byte{0x07},
		[]byte{0xff, 0xff, 0xff},
		[]byte{},
	)
	fuzzStdOracle(f, "inflate", seeds, func(t *testing.T, input []byte, summary string) {
		agreeWithGo(t, "inflate", input, summary, goInflate(input))
	})
}

// FuzzSelfHostStdGunzip feeds hostile bytes to the gzip decoder of std/deflate
// and to compress/gzip, and requires the two to agree.
func FuzzSelfHostStdGunzip(f *testing.F) {
	seeds := withDamage(compressedSeeds(writeGzip))
	two := append(append([]byte(nil), seeds[1]...), seeds[1]...)
	seeds = append(seeds, two, []byte{0x1f, 0x8b}, []byte{0x1f, 0x8b, 8, 0xff, 0, 0, 0, 0, 0, 0}, []byte{0x1f, 0x8b, 7})
	fuzzStdOracle(f, "gunzip", seeds, func(t *testing.T, input []byte, summary string) {
		agreeWithGo(t, "gunzip", input, summary, goGunzip(input))
	})
}

// FuzzSelfHostStdZlib feeds hostile bytes to the zlib decoder of std/deflate
// and to compress/zlib, and requires the two to agree.
func FuzzSelfHostStdZlib(f *testing.F) {
	seeds := withDamage(compressedSeeds(writeZlib))
	var dict bytes.Buffer
	zw, err := zlib.NewWriterLevelDict(&dict, zlib.BestCompression, []byte("hello"))
	if err != nil {
		f.Fatal(err)
	}
	zw.Write([]byte("hello hello"))
	zw.Close()
	seeds = append(seeds, dict.Bytes(), []byte{0x78}, []byte{0x78, 0x9c}, []byte{0x08, 0x1d, 0x03, 0x00, 0x00, 0x00, 0x00, 0x01}, []byte{0x7f, 0x9c})
	fuzzStdOracle(f, "zlib", seeds, func(t *testing.T, input []byte, summary string) {
		agreeWithGo(t, "zlib", input, summary, goZlib(input))
	})
}

func roundTrips(t *testing.T, reader string, input []byte, summary string) {
	t.Helper()
	if !strings.HasSuffix(summary, " roundtrip ok") {
		t.Errorf("%s does not give back %d bytes it compressed: %q\ninput: %x", reader, len(input), summary, input)
	}
}

// codecSeeds gives plain inputs for the round trip, and the frames of the
// codec's own test.
func codecSeeds(f *testing.F, frames ...string) [][]byte {
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
	seeds := codecSeeds(f,
		"28b52ffd201aa500007068656c6c6f2c2020776f726c640a0100e28a11",
		"28b52ffda4400d03005400001061610100fbff39c002036a08618356bc98",
	)
	seeds = append(seeds, []byte{0x28, 0xb5, 0x2f, 0xfd}, []byte{0x50, 0x2a, 0x4d, 0x18, 4, 0, 0, 0, 1, 2, 3, 4})
	fuzzStdOracle(f, "zstd", seeds, func(t *testing.T, input []byte, summary string) {
		roundTrips(t, "zstd", input, summary)
	})
}

// FuzzSelfHostStdBrotli feeds hostile bytes to the Brotli decoder of
// std/brotli, and compresses each input with std/brotli and decodes it back.
func FuzzSelfHostStdBrotli(f *testing.F) {
	seeds := codecSeeds(f,
		"8b0c8068656c6c6f2c2068656c6c6f2c2068656c6c6f20776f726c640a03",
		"2c0161626303",
	)
	seeds = append(seeds, []byte{0x3b}, []byte{0x06}, []byte{0xff})
	fuzzStdOracle(f, "brotli", seeds, func(t *testing.T, input []byte, summary string) {
		roundTrips(t, "brotli", input, summary)
	})
}
