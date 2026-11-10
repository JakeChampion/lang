package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The targets here feed hostile bytes to three more readers of the standard
// library through tests/fuzz/stdparse.fern: the DNS message reader of
// std/dns, the protobuf reader of std/protobuf and the JPEG decoder of
// std/jpeg. A resolver reads DNS replies from the network, and a program
// decodes the images and the protobuf messages that a peer sends. The DNS
// target encodes each message it decodes and decodes it again, and the two
// decodes must agree. The JPEG target decodes each input with Go's
// image/jpeg as well, and when both accept it the dimensions must agree.

// runStdReader runs one stdparse reader on input under the fuzz budgets and
// gives its summary.
func runStdReader(t *testing.T, bin, reader string, input []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, input, 0o644); err != nil {
		t.Fatal(err)
	}
	r := runUnderBudget(exec.Command(bin, reader, path))
	if failOutsideBudget(t, "stdparse "+reader, r, input) || r.code != 0 {
		t.Fatalf("stdparse %s: exit %d\nstdout: %q\nstderr: %q", reader, r.code, r.stdout, r.stderr)
	}
	return strings.TrimSpace(r.stdout)
}

// dnsName writes a domain name as length-prefixed labels.
func dnsName(name string) []byte {
	var b []byte
	for _, label := range strings.Split(name, ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	return append(b, 0)
}

// dnsMessage writes a header with the given counts and flags, then body.
func dnsMessage(id, flags uint16, qd, an, ns, ar int, body ...[]byte) []byte {
	h := make([]byte, 12)
	binary.BigEndian.PutUint16(h[0:], id)
	binary.BigEndian.PutUint16(h[2:], flags)
	binary.BigEndian.PutUint16(h[4:], uint16(qd))
	binary.BigEndian.PutUint16(h[6:], uint16(an))
	binary.BigEndian.PutUint16(h[8:], uint16(ns))
	binary.BigEndian.PutUint16(h[10:], uint16(ar))
	for _, b := range body {
		h = append(h, b...)
	}
	return h
}

func dnsRR(name []byte, rtype, class uint16, ttl uint32, data []byte) []byte {
	b := append([]byte(nil), name...)
	b = binary.BigEndian.AppendUint16(b, rtype)
	b = binary.BigEndian.AppendUint16(b, class)
	b = binary.BigEndian.AppendUint32(b, ttl)
	b = binary.BigEndian.AppendUint16(b, uint16(len(data)))
	return append(b, data...)
}

func dnsQuestion(name string, qtype uint16) []byte {
	return binary.BigEndian.AppendUint16(binary.BigEndian.AppendUint16(dnsName(name), qtype), 1)
}

// FuzzSelfHostStdDNS feeds hostile bytes to the DNS message reader, and
// requires a message that it decodes to survive an encode and a second decode.
func FuzzSelfHostStdDNS(f *testing.F) {
	fuzzReaderTarget(f)
}

func dnsSeeds(testing.TB) [][]byte {
	q := dnsQuestion("example.com", 1)
	ptr := []byte{0xc0, 12}
	seeds := [][]byte{
		dnsMessage(0x1234, 0x0100, 1, 0, 0, 0, q),
		dnsMessage(0x1234, 0x8180, 1, 3, 0, 0, q,
			dnsRR(ptr, 1, 1, 300, []byte{93, 184, 216, 34}),
			dnsRR(ptr, 28, 1, 300, bytes.Repeat([]byte{0x20, 0x01}, 8)),
			dnsRR(ptr, 5, 1, 60, dnsName("www.example.org"))),
		dnsMessage(1, 0x8183, 1, 0, 1, 0, q, dnsRR(dnsName("com"), 6, 1, 900, bytes.Repeat([]byte{1}, 22))),
		dnsMessage(2, 0x8180, 1, 1, 0, 1, q, dnsRR(ptr, 12, 1, 1, []byte{3, 'f', 'o', 'o', 0xc0, 12}), dnsRR([]byte{0}, 41, 1232, 0, nil)),
		dnsMessage(3, 0x8180, 1, 1, 0, 0, q, dnsRR([]byte{0xc0, 0x1d}, 1, 1, 1, []byte{1, 2, 3, 4})),
		dnsMessage(4, 0x8180, 1, 1, 0, 0, q, dnsRR([]byte{0xc0, 0xff}, 1, 1, 1, []byte{1, 2, 3, 4})),
		dnsMessage(5, 0x0100, 1, 0, 0, 0, append([]byte{64}, bytes.Repeat([]byte{'a'}, 64)...)),
		dnsMessage(6, 0x0100, 1, 0, 0, 0, bytes.Repeat([]byte{63, 'b'}, 3)),
		dnsMessage(7, 0x0100, 9, 0, 0, 0, q),
		dnsMessage(8, 0x8180, 1, 1, 0, 0, q, dnsRR(ptr, 1, 1, 1, []byte{1, 2, 3})),
		dnsMessage(9, 0xffff, 0, 0, 0, 0),
		dnsMessage(0x1234, 0x0100, 1, 0, 0, 0, []byte{7, 'e', '^', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', '.', 0, 0, 1, 0, 1}),
		[]byte{0x12, 0x34},
		{},
	}
	return seeds
}

func dnsRoundTrips(r readerRun) string {
	if strings.Contains(r.summary, "roundtrip broken") {
		return fmt.Sprintf("dns encodes a message it decoded into one that decodes differently: %q\ninput: %x", r.summary, r.input)
	}
	return ""
}

// pbVarint writes one protobuf varint.
func pbVarint(v uint64) []byte {
	return binary.AppendUvarint(nil, v)
}

func pbField(number, wire int, payload []byte) []byte {
	b := pbVarint(uint64(number<<3 | wire))
	if wire == 2 {
		b = append(b, pbVarint(uint64(len(payload)))...)
	}
	return append(b, payload...)
}

// FuzzSelfHostStdProtobuf feeds hostile bytes to the protobuf reader, and the
// length-delimited fields it finds to the reader again.
func FuzzSelfHostStdProtobuf(f *testing.F) {
	fuzzReaderTarget(f)
}

func protobufSeeds(testing.TB) [][]byte {
	inner := append(pbField(1, 0, pbVarint(150)), pbField(2, 2, []byte("hi"))...)
	return [][]byte{
		pbField(1, 0, pbVarint(150)),
		append(append(pbField(1, 0, pbVarint(1<<63)), pbField(2, 2, inner)...), pbField(3, 1, make([]byte, 8))...),
		pbField(4, 5, []byte{1, 2, 3, 4}),
		pbField(5, 2, bytes.Repeat(inner, 50)),
		pbField(6, 3, nil),
		pbField(7, 4, nil),
		pbField(8, 6, nil),
		append(pbVarint(9<<3|2), pbVarint(1<<40)...),
		bytes.Repeat([]byte{0xff}, 11),
		pbField(1, 1, []byte{1, 2, 3}),
		[]byte{0},
		pbVarint(uint64(1) << 32 << 3),
		{},
	}
}

// imageVectorJPEGs reads the encoded JPEG files of tests/stdlib/image_vectors.fern,
// which Pillow wrote.
func imageVectorJPEGs(f testing.TB) [][]byte {
	f.Helper()
	src, err := os.ReadFile(e2eharness.RepoPath("tests", "stdlib", "image_vectors.fern"))
	if err != nil {
		f.Fatal(err)
	}
	fn := regexp.MustCompile(`(?s)pub function jpeg_\w+\(\): u8\[\] \{\s*return \[(.*?)\];`)
	num := regexp.MustCompile(`(\d+) as u8`)
	var out [][]byte
	for _, m := range fn.FindAllSubmatch(src, -1) {
		var b []byte
		for _, n := range num.FindAllSubmatch(m[1], -1) {
			v, err := strconv.Atoi(string(n[1]))
			if err != nil || v > 255 {
				f.Fatalf("image_vectors.fern: bad byte %q", n[1])
			}
			b = append(b, byte(v))
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		f.Fatal("image_vectors.fern has no JPEG vectors")
	}
	return out
}

// goJPEGs gives JPEG files that Go's encoder wrote: colour gradients at three
// qualities and sizes that are not a multiple of the block size, and gray.
func goJPEGs(f testing.TB) [][]byte {
	f.Helper()
	var out [][]byte
	enc := func(img image.Image, q int) {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: q}); err != nil {
			f.Fatal(err)
		}
		out = append(out, b.Bytes())
	}
	for _, size := range [][2]int{{16, 16}, {33, 17}, {1, 1}, {64, 8}} {
		img := image.NewRGBA(image.Rect(0, 0, size[0], size[1]))
		for y := 0; y < size[1]; y++ {
			for x := 0; x < size[0]; x++ {
				img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 13), uint8(x * y), 255})
			}
		}
		for _, q := range []int{10, 75, 100} {
			enc(img, q)
		}
	}
	gray := image.NewGray(image.Rect(0, 0, 24, 9))
	for i := range gray.Pix {
		gray.Pix[i] = uint8(i * 3)
	}
	enc(gray, 80)
	return out
}

// hugeJPEG gives a baseline frame header that declares an image of 65535 by
// 65535 pixels, with no data after it.
func hugeJPEG(base []byte) []byte {
	b := append([]byte(nil), base...)
	for i := 0; i+8 < len(b); i++ {
		if b[i] == 0xff && (b[i+1] == 0xc0 || b[i+1] == 0xc2) {
			b[i+5], b[i+6], b[i+7], b[i+8] = 0xff, 0xff, 0xff, 0xff
			return b
		}
	}
	return b
}

// FuzzSelfHostStdJPEG feeds hostile bytes to the JPEG decoder. When Go's
// image/jpeg and the decoder both accept an input, they must agree on its
// width and height.
func FuzzSelfHostStdJPEG(f *testing.F) {
	fuzzReaderTarget(f)
}

func jpegSeeds(f testing.TB) [][]byte {
	seeds := append(goJPEGs(f), imageVectorJPEGs(f)...)
	base := seeds[0]
	seeds = append(seeds, damaged(base)...)
	seeds = append(seeds,
		hugeJPEG(base),
		[]byte{0xff, 0xd8},
		[]byte{0xff, 0xd8, 0xff, 0xd9},
		[]byte{0xff, 0xd8, 0xff, 0xc3, 0, 11, 8, 0, 1, 0, 1, 1, 1, 0x11, 0},
		[]byte{0, 1},
		[]byte{},
	)
	return seeds
}

func jpegAgreesWithGo(r readerRun) string {
	reader, input := r.reader, r.input
	fields := strings.Fields(r.summary)
	if len(fields) < 4 || fields[1] != "ok" {
		return ""
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(input))
	if err != nil {
		return ""
	}
	if _, err := jpeg.Decode(bytes.NewReader(input)); err != nil {
		return ""
	}
	if want := strconv.Itoa(cfg.Width) + " " + strconv.Itoa(cfg.Height); fields[2]+" "+fields[3] != want {
		return fmt.Sprintf("%s decodes %d bytes as %s by %s, Go decodes them as %s\ninput: %x", reader, len(input), fields[2], fields[3], want, input)
	}
	return ""
}

// TestSelfHostStdJPEGAgainstGo decodes the files that Go's encoder wrote and
// the Pillow vectors, and requires the dimensions that Go reads.
func TestSelfHostStdJPEGAgainstGo(t *testing.T) {
	bin := fuzzStdParse(t)
	for i, in := range append(goJPEGs(t), imageVectorJPEGs(t)...) {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(in))
		if err != nil {
			t.Fatalf("file %d: Go refuses it: %v", i, err)
		}
		got := runStdReader(t, bin, "jpeg", in)
		want := "jpeg ok " + strconv.Itoa(cfg.Width) + " " + strconv.Itoa(cfg.Height) + " "
		if !strings.HasPrefix(got, want) {
			t.Errorf("file %d: stdparse jpeg gives %q, want the prefix %q", i, got, want)
		}
	}
	if got := runStdReader(t, bin, "jpeg", hugeJPEG(goJPEGs(t)[0])); !strings.HasPrefix(got, "jpeg refused") {
		t.Errorf("a frame of 65535 by 65535 pixels with no data gives %q, want a refusal", got)
	}
}
