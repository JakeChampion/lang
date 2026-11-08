package e2ecompiler

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The targets here feed hostile bytes to the network-facing parsers of the
// standard library through tests/fuzz/stdparse.fern: the HTTP/1 request
// framer and its chunked-body decoder, the HPACK decoder, the JSON parser,
// and the URL and query parsers. A server reads these bytes from its peers.
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
