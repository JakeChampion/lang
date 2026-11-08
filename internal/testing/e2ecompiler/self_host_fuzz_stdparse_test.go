package e2ecompiler

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The targets here feed hostile bytes to the network-facing parsers of the
// standard library through tests/fuzz/stdparse.fern: the HTTP/1 request
// framer and its chunked-body decoder, the HTTP/2 frame reader and server
// connection, the HPACK and QPACK decoders, the QUIC packet and frame readers,
// the JSON parser, and the URL and query parsers. A server reads these bytes from its peers.
// The shipped CLI builds the program, so the targets fuzz the standard
// library as this tree compiles it. Each parser refuses malformed bytes with
// an error value, so the one accepted outcome is exit 0 with a summary.

func fuzzStdParse(f *testing.F) string {
	fuzzBuildCache(f)
	if _, runner := e2eharness.X86_64Tooling(f); len(runner) != 0 {
		f.Skip("the fuzz programs run natively only")
	}
	return e2eharness.CachedCLIBuild(f, e2eharness.CLIBuild{
		Target: e2eharness.TargetX86_64Linux,
		Dir:    e2eharness.RepoPath("tests", "fuzz"),
		Entry:  "stdparse.fern",
	})
}

func fuzzStdParser(f *testing.F, reader string, seeds [][]byte) {
	fuzzBinReader(f, fuzzStdParse(f), "stdparse", reader, seeds)
}

func hexSeed(f *testing.F, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		f.Fatal(err)
	}
	return b
}

// FuzzSelfHostStdHTTP feeds hostile bytes to the HTTP/1 request framer, and
// the same bytes to the chunked-body decoder in pieces of seven.
func FuzzSelfHostStdHTTP(f *testing.F) {
	fuzzStdParser(f, "http", [][]byte{
		[]byte("GET /a?b=c HTTP/1.1\r\nHost: example.com\r\n\r\n"),
		[]byte("POST /p HTTP/1.1\r\nHost: a\r\nContent-Length: 5\r\n\r\nhello"),
		[]byte("POST /p HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"),
		[]byte("5\r\nhello\r\n6;ext=1\r\n world\r\n0\r\nX-Trailer: 1\r\n\r\nNEXT"),
		[]byte("POST / HTTP/1.1\r\nHost: a\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"),
		[]byte("POST / HTTP/1.1\r\nHost: a\r\nContent-Length: 4\r\nContent-Length: 5\r\n\r\nabcd"),
		[]byte("POST / HTTP/1.1\r\nHost: a\r\nContent-Length: -1\r\n\r\n"),
		[]byte("ffffffffffffffff\r\nx\r\n"),
		[]byte("-1\r\n\r\n"),
		[]byte("GET / HTTP/1.1\r\nHost: a\r\nX-Folded: one\r\n two\r\n\r\n"),
		[]byte("GET / HTTP/1.1\nHost: a\n\n"),
		[]byte("GET / HTTP/1.1\r\nHost: a\r\nNoColon\r\n\r\n"),
		[]byte("GET / HTTP/1.1\r\nHost: a\x00b\r\n\r\n"),
		[]byte("POST / HTTP/1.1\r\nHost: a\r\nExpect: 100-continue\r\nContent-Length: 3\r\n\r\n"),
		[]byte("GET /" + strings.Repeat("a", 9000) + " HTTP/1.1\r\nHost: a\r\n\r\n"),
		[]byte("GET / HTTP/1.1\r\n" + strings.Repeat("X-A: b\r\n", 200) + "\r\n"),
		[]byte("GET / HTTP/1.1\r\nHost: a\r\n\r\nGET / HTTP/1.1\r\nHost: b\r\n\r\n"),
		[]byte("GET / HTTP/9.9\r\n\r\n"),
		[]byte(""),
	})
}

// FuzzSelfHostStdHPACK feeds hostile header blocks to the HPACK decoder, twice on one
// decoder so the second block reads the dynamic table the first one filled.
func FuzzSelfHostStdHPACK(f *testing.F) {
	fuzzStdParser(f, "hpack", [][]byte{
		hexSeed(f, "400a637573746f6d2d6b65790d637573746f6d2d686561646572"),
		hexSeed(f, "828684418cf1e3c2e5f23a6ba0ab90f4ff"),
		hexSeed(f, "828684be5886a8eb10649cbf"),
		hexSeed(f, "3fe11f"),
		hexSeed(f, "3fffffff0f"),
		hexSeed(f, "1fffffffffffffff7f"),
		hexSeed(f, "80"),
		hexSeed(f, "ff"),
		hexSeed(f, "400a6375"),
		hexSeed(f, "408aff"),
		hexSeed(f, "40"+"7f"+"ffff03"),
		[]byte(""),
	})
}

// h2Frame lays out one HTTP/2 frame: a 24-bit length, a type, flags and a
// 31-bit stream identifier before the payload.
func h2Frame(kind, flags byte, stream uint32, payload []byte) []byte {
	n := len(payload)
	head := []byte{byte(n >> 16), byte(n >> 8), byte(n), kind, flags, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(head[5:], stream)
	return append(head, payload...)
}

// FuzzSelfHostStdH2 feeds hostile bytes to the HTTP/2 frame reader, and the
// client preface and the same bytes in pieces of nine to a server connection.
func FuzzSelfHostStdH2(f *testing.F) {
	block := hexSeed(f, "828684418cf1e3c2e5f23a6ba0ab90f4ff")
	settings := h2Frame(4, 0, 0, nil)
	headers := h2Frame(1, 5, 1, block)
	rst := h2Frame(3, 0, 1, []byte{0, 0, 0, 8})
	var rapidReset bytes.Buffer
	rapidReset.Write(settings)
	for id := uint32(1); id < 2000; id += 2 {
		rapidReset.Write(h2Frame(1, 5, id, block))
		rapidReset.Write(h2Frame(3, 0, id, []byte{0, 0, 0, 8}))
	}
	fuzzStdParser(f, "h2", [][]byte{
		append(append([]byte{}, settings...), headers...),
		append(append(append([]byte{}, settings...), h2Frame(4, 1, 0, nil)...), h2Frame(6, 0, 0, make([]byte, 8))...),
		append(append([]byte{}, settings...), append(h2Frame(1, 4, 1, block[:5]), h2Frame(9, 4, 1, block[5:])...)...),
		append(append([]byte{}, settings...), h2Frame(9, 4, 1, block)...),
		append(append([]byte{}, settings...), h2Frame(8, 0, 0, []byte{0, 0, 0, 0})...),
		append(append([]byte{}, settings...), h2Frame(0, 0, 0, []byte("a"))...),
		append(append([]byte{}, settings...), h2Frame(0, 8, 1, []byte{5, 'a'})...),
		append(append([]byte{}, settings...), h2Frame(2, 0, 1, []byte{0, 0, 0, 1, 16})...),
		append(append([]byte{}, settings...), h2Frame(4, 0, 0, []byte{0, 5, 0, 0, 0, 1})...),
		append(append([]byte{}, settings...), h2Frame(7, 0, 0, []byte{0, 0, 0, 1, 0, 0, 0, 2, 'b', 'y', 'e'})...),
		append(append(append([]byte{}, settings...), headers...), rst...),
		rapidReset.Bytes(),
		{0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 1},
		[]byte(""),
	})
}

// FuzzSelfHostStdQPACK feeds hostile bytes to the QPACK decoder. The first
// byte gives the length of the encoder-stream bytes that follow it, and the
// rest is one header section on stream 0.
func FuzzSelfHostStdQPACK(f *testing.F) {
	fuzzStdParser(f, "qpack", [][]byte{
		hexSeed(f, "000000d1d7"),
		hexSeed(f, "073fe11f41610162020080"),
		hexSeed(f, "00060080"),
		hexSeed(f, "00ffffffffffffffffff0f"),
		hexSeed(f, "053fffffff0f0000"),
		hexSeed(f, "01000000"),
		hexSeed(f, "0000"),
		hexSeed(f, "00"),
		[]byte(""),
	})
}

// quicInitial seals payload as a QUIC version 1 client Initial packet for
// dcid, with a 4-byte packet number 0, as RFC 9001 sections 5.2 to 5.4 lay out.
func quicInitial(f *testing.F, dcid, payload []byte) []byte {
	salt := hexSeed(f, "38762cf7f55934b34d179ae6a4c80cadccbb7f0a")
	expand := func(secret []byte, label string, n int) []byte {
		full := "tls13 " + label
		info := append([]byte{byte(n >> 8), byte(n), byte(len(full))}, full...)
		out, err := hkdf.Expand(sha256.New, secret, string(append(info, 0)), n)
		if err != nil {
			f.Fatal(err)
		}
		return out
	}
	initial, err := hkdf.Extract(sha256.New, dcid, salt)
	if err != nil {
		f.Fatal(err)
	}
	client := expand(initial, "client in", 32)
	key, iv, hp := expand(client, "quic key", 16), expand(client, "quic iv", 12), expand(client, "quic hp", 16)
	header := []byte{0xc3, 0, 0, 0, 1, byte(len(dcid))}
	header = append(header, dcid...)
	n := 4 + len(payload) + 16
	header = append(header, 0, 0, 0x40|byte(n>>8), byte(n), 0, 0, 0, 0)
	block, err := aes.NewCipher(key)
	if err != nil {
		f.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		f.Fatal(err)
	}
	sealed := gcm.Seal(nil, iv, payload, header)
	hpBlock, err := aes.NewCipher(hp)
	if err != nil {
		f.Fatal(err)
	}
	mask := make([]byte, 16)
	hpBlock.Encrypt(mask, sealed[:16])
	header[0] ^= mask[0] & 0x0f
	for i := 0; i < 4; i++ {
		header[len(header)-4+i] ^= mask[1+i]
	}
	return append(header, sealed...)
}

// FuzzSelfHostStdQUIC feeds hostile datagrams to the QUIC packet reader: it
// walks the coalesced packets and opens each Initial packet with the client
// Initial keys of its destination connection ID.
func FuzzSelfHostStdQUIC(f *testing.F) {
	dcid := hexSeed(f, "8394c8f03e515708")
	payload := append([]byte{6, 0, 5, 'h', 'e', 'l', 'l', 'o', 1}, make([]byte, 40)...)
	initial := quicInitial(f, dcid, payload)
	fuzzStdParser(f, "quic", [][]byte{
		initial,
		append(append([]byte{}, initial...), initial...),
		append(append([]byte{}, initial[:len(initial)-1]...), initial[len(initial)-1]^1),
		initial[:30],
		append([]byte{0x40}, append(dcid, make([]byte, 24)...)...),
		append([]byte{0x80, 0, 0, 0, 0, 8}, append(dcid, 0, 0, 0, 0, 1, 0xff, 0, 0, 1)...),
		append([]byte{0xf0, 0, 0, 0, 1, 8}, append(dcid, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15)...),
		{0xc0, 0, 0, 0, 1, 21},
		{0x80, 0, 0, 0, 1, 0, 0, 0, 0x7f, 0xff},
		append([]byte{0xc3, 0, 0, 0, 1, 8}, append(dcid, 0, 0, 0x5f, 0xff, 0, 0, 0, 0)...),
		{0xc3, 0xfa, 0xce, 0xb0, 0x0c, 0, 0},
		[]byte(""),
	})
}

// FuzzSelfHostStdQUICFrames feeds hostile packet payloads to the QUIC frame
// reader.
func FuzzSelfHostStdQUICFrames(f *testing.F) {
	fuzzStdParser(f, "frames", [][]byte{
		{0, 0, 1},
		{2, 10, 0, 1, 5, 2, 3, 1, 1},
		{3, 10, 0, 0, 5, 1, 2, 3},
		{2, 0x80, 0, 0, 0, 0, 0x3f, 0xff, 0xff, 0},
		{6, 0, 5, 'h', 'e', 'l', 'l', 'o'},
		{0x0f, 4, 0x40, 0x10, 3, 'a', 'b', 'c'},
		{0x08, 4, 'a', 'b'},
		{0x0e, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xc0, 0, 0, 0, 0, 0, 0, 1, 1, 'x'},
		{4, 4, 7, 100},
		{0x10, 0x7f, 0xff},
		{0x18, 1, 0, 21, 1, 2, 3},
		{0x18, 1, 0, 8, 1, 2, 3, 4, 5, 6, 7, 8, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		{0x1c, 10, 0, 3, 0xff, 0xfe, 0xfd},
		{0x1e},
		{0x40, 0x50, 0},
		{0x15, 0x40},
		[]byte(""),
	})
}

// FuzzSelfHostStdJSON feeds hostile documents to the JSON parser.
func FuzzSelfHostStdJSON(f *testing.F) {
	fuzzStdParser(f, "json", [][]byte{
		[]byte(`{"a": [1, 2.5e10, -0, true, null, "xé😀"], "b": {}}`),
		[]byte(`"\ud800"`),
		[]byte(`"\udc00\ud800"`),
		[]byte("\"\x01\""),
		[]byte(`[1, 2,]`),
		[]byte(`{"a": 1,}`),
		[]byte(`{"a" 1}`),
		[]byte(`NaN`),
		[]byte(`1e999999999`),
		[]byte(`-` + strings.Repeat("9", 400)),
		[]byte(strings.Repeat("[", 129) + strings.Repeat("]", 129)),
		[]byte(strings.Repeat(`{"a":`, 200)),
		[]byte(`{"a": 1, "a": 2}`),
		[]byte("\xef\xbb\xbf{}"),
		[]byte("\xff"),
		[]byte(""),
	})
}

// FuzzSelfHostStdURL feeds hostile strings to the URL parser and the query parser.
func FuzzSelfHostStdURL(f *testing.F) {
	fuzzStdParser(f, "url", [][]byte{
		[]byte("https://user:pass@example.com:8080/p/a/t/h?q=1&r=two#frag"),
		[]byte("http://[::1]:80/"),
		[]byte("http://[::1"),
		[]byte("http://example.com:99999/"),
		[]byte("http://example.com:-1/"),
		[]byte("a=1&a=2&b&=c&d=%zz&e=%"),
		[]byte("%"),
		[]byte("javascript:alert(1)"),
		[]byte("http://a@b@c/"),
		[]byte("http://exa mple.com/"),
		[]byte("//example.com/x"),
		[]byte("http://example.com/" + strings.Repeat("%41", 5000)),
		[]byte("http://\xff/"),
		[]byte(""),
	})
}
