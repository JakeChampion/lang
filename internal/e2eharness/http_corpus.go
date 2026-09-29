package e2eharness

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// HTTPCorpusCase is one request of the parser corpus under
// internal/e2e/testdata/http-corpus: the wire bytes of an upstream parser's
// test fixture, what that parser made of them, and what std/http's parser
// is pinned to make of them.
type HTTPCorpusCase struct {
	Name     string
	Kind     string // the upstream fixture's mode, e.g. `request-lenient-headers`
	Upstream string // ok, error or partial, as the upstream test expects
	Wire     []byte
	Verdict  string // std/http's pinned answer; empty when not yet recorded
}

// HTTPCorpusCases reads every corpus file under dir, in file then line order.
func HTTPCorpusCases(t *testing.T, dir string) []HTTPCorpusCase {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	var cases []HTTPCorpusCase
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			cols := strings.Split(line, "\t")
			if len(cols) != 5 {
				t.Fatalf("%s: %q has %d columns, want 5", path, line, len(cols))
			}
			wire, err := hex.DecodeString(cols[3])
			if err != nil {
				t.Fatalf("%s: %s: %v", path, cols[0], err)
			}
			cases = append(cases, HTTPCorpusCase{Name: cols[0], Kind: cols[1], Upstream: cols[2], Wire: wire, Verdict: cols[4]})
		}
		f.Close()
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if len(cases) == 0 {
		t.Fatalf("no corpus cases under %s", dir)
	}
	return cases
}

// HTTPCorpusSource is a program that parses each case with
// `http_parse_request_framed` and prints its name and verdict, one line
// each, in corpus order. The wire arrives hex-encoded so no byte has to
// survive a string literal, and a path is printed with every byte outside
// visible ASCII as `%XX`, so a verdict is one printable line.
func HTTPCorpusSource(cases []HTTPCorpusCase) string {
	var b strings.Builder
	b.WriteString(`import "std/http";
import "std/hex";
import "std/i32";
import "std/string";

function flag(b: boolean): string {
    if (b) { return "1"; }
    return "0";
}

// A wire refused with 400 is tried again with a Host header behind its
// request line (past one empty line before it): a fixture written for a
// parser that does not enforce the Host rule (RFC 9112 §3.2) otherwise
// hides every other answer behind it.
function verdict(wire: u8[]): string {
    var v: string = answer(wire);
    if (v != "malformed 400") { return v; }
    var line: i32 = 0;
    if (find_crlf(wire) == 0) { line = 2; }
    var crlf: i32 = find_crlf(wire.drop(line));
    if (crlf < 0) { return v; }
    var with_host: u8[] = wire.take(line + crlf + 2).concat("Host: corpus\r\n".bytes()).concat(wire.drop(line + crlf + 2));
    var again: string = answer(with_host);
    if (again == "malformed 400") { return v; }
    return "nohost " + again;
}

function find_crlf(wire: u8[]): i32 {
    var i: i32 = 0;
    while (i + 1 < wire.len()) {
        if (wire[i] == (13 as u8) && wire[i + 1] == (10 as u8)) { return i; }
        i = i + 1;
    }
    return -1;
}

// shown(path) — the path with every byte outside visible ASCII, and the
// percent sign itself, as a percent and two hex digits, so a verdict is one
// printable line whatever the target decoded to.
function shown(path: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < path.len()) {
        var b: u8 = path[i];
        if (b > (32 as u8) && b < (127 as u8) && b != (37 as u8)) {
            out = out + slice_unchecked(path, i, i + 1);
        } else {
            out = out + "%" + hex.hex_encode_upper([b]);
        }
        i = i + 1;
    }
    return out;
}

function answer(wire: u8[]): string {
    match (http.http_parse_request_framed(wire)) {
        Framed(f) => {
            return "ok " + f.request.method + " " + shown(f.request.path)
                + " h=" + f.request.headers.len().to_string()
                + " b=" + f.request.body_len().to_string()
                + " t=" + f.request.trailers.len().to_string()
                + " len=" + f.len.to_string()
                + " ka=" + flag(f.keep_alive);
        },
        Incomplete => { return "incomplete"; },
        Continue => { return "continue"; },
        Malformed(status) => { return "malformed " + status.to_string(); }
    }
    return "";
}

function main(): i32 {
`)
	for _, c := range cases {
		fmt.Fprintf(&b, "    print(%q + \"\\t\" + verdict(hex.hex_decode(\"%s\")));\n", c.Name, hex.EncodeToString(c.Wire))
	}
	b.WriteString("    return 0;\n}\n")
	return b.String()
}

// HTTPCorpusDiff names each case whose measured line differs from its
// pinned one, with both, or the shape mismatch when the line counts do.
func HTTPCorpusDiff(cases []HTTPCorpusCase, got string) []string {
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != len(cases) {
		return []string{fmt.Sprintf("%d lines of output for %d cases", len(lines), len(cases))}
	}
	var diffs []string
	for i, c := range cases {
		want := c.Name + "\t" + c.Verdict
		if lines[i] != want {
			diffs = append(diffs, fmt.Sprintf("%s: got %q, pinned %q", c.Name, strings.TrimPrefix(lines[i], c.Name+"\t"), c.Verdict))
		}
	}
	return diffs
}
