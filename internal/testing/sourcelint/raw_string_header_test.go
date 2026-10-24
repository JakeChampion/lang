package sourcelint

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var rawStringCallRe = regexp.MustCompile(`__raw_string\(`)

var rawAllocBindRe = regexp.MustCompile(`let\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*usize\s*=\s*__raw_alloc\(`)

var genFnRe = regexp.MustCompile(`(?m)^(?:pub )?function\s+[A-Za-z_][A-Za-z0-9_]*\s*\(`)

func TestRawStringPointerComesFromRawAlloc(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	path := filepath.Join(root, "compiler", "asmcore.fern")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read asmcore.fern: %v", err)
	}
	src := blankFullLineComments(string(raw))

	for _, chunk := range splitGeneratorFns(src) {
		allocated := map[string]bool{}
		for _, m := range rawAllocBindRe.FindAllStringSubmatch(chunk.text, -1) {
			allocated[m[1]] = true
		}
		for _, loc := range rawStringCallRe.FindAllStringIndex(chunk.text, -1) {
			open := loc[1] - 1
			arg, ok := firstArg(chunk.text, open)
			if !ok {
				t.Errorf("asmcore.fern:%d: unbalanced parentheses after __raw_string(",
					lineOf(src, chunk.off+open))
				continue
			}
			name := strings.TrimSpace(arg)
			if !bareAddrArgRe.MatchString(name) {
				t.Errorf("asmcore.fern:%d: __raw_string's pointer is `%s` — it has to be "+
					"the bare name of a __raw_alloc result, whose reserved 24-byte header "+
					"the box is stamped into (#7351)", lineOf(src, chunk.off+open), name)
				continue
			}
			bare := strings.Trim(strings.TrimSpace(strings.Trim(name, `"`)), "+ ")
			if !allocated[bare] {
				t.Errorf("asmcore.fern:%d: __raw_string boxes %q, which %s does not bind "+
					"with __raw_alloc. __raw_alloc is the ONLY producer that reserves the "+
					"24-byte string-box header ahead of its pointer; boxing a scratch "+
					"buffer, an argv entry or a borrowed address writes the box over 24 "+
					"bytes that belong to something else, and nothing reports it at run "+
					"time (#7351)", lineOf(src, chunk.off+open), bare, chunk.name)
			}
		}
	}
}

type genFnChunk struct {
	name string
	off  int
	text string
}

func splitGeneratorFns(src string) []genFnChunk {
	locs := genFnRe.FindAllStringIndex(src, -1)
	out := make([]genFnChunk, 0, len(locs))
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		header := src[loc[0]:loc[1]]
		name := strings.TrimSuffix(strings.Fields(strings.TrimPrefix(header, "pub "))[1], "(")
		out = append(out, genFnChunk{name: name, off: loc[0], text: src[loc[0]:end]})
	}
	return out
}

var bareAddrArgRe = regexp.MustCompile(`^(?:"\s*\+\s*)?[A-Za-z_][A-Za-z0-9_]*(?:\s*\+\s*")?$`)

func firstArg(src string, open int) (string, bool) {
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return src[open+1 : i], true
			}
		case ',':
			if depth == 1 {
				return src[open+1 : i], true
			}
		}
	}
	return "", false
}

func lineOf(src string, off int) int {
	return strings.Count(src[:off], "\n") + 1
}

func blankFullLineComments(src string) string {
	out := []byte(src)
	for i := 0; i < len(out); {
		end := bytes.IndexByte(out[i:], '\n')
		if end < 0 {
			end = len(out)
		} else {
			end += i
		}
		j := i
		for j < end && (out[j] == ' ' || out[j] == '\t') {
			j++
		}
		if j+1 < end && out[j] == '/' && out[j+1] == '/' {
			for k := j; k < end; k++ {
				out[k] = ' '
			}
		}
		i = end + 1
	}
	return string(out)
}
