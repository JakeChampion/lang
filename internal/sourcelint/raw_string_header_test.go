package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `__raw_alloc(n)` reserves 24 bytes AHEAD of the pointer it returns, and
// `__raw_string(p, len)` builds the string box in that reserved header rather
// than allocating a second block (#7351: a heap string used to cost a box block
// and a data block where native's costs one). Both register backends do this;
// wasm refuses the whole raw floor and serves these helpers with hand-written
// WAT, so it is unaffected.
//
// The header is therefore a CONTRACT on `__raw_string`'s pointer: it must be a
// `__raw_alloc` result, unadjusted. Hand it anything else — the static
// `__raw_scratch` buffer, an argv entry, an interior address — and the box is
// stamped into 24 bytes that belong to something else. Nothing at run time
// reports that; the corruption surfaces somewhere later.
//
// asmcore.fern is the only place the contract can be broken, because it is the
// only place the raw floor is written: every rt_src_* helper body is a Fern
// source string this file generates, and the intrinsics exist for those bodies.
//
// The scan is per generator function, which is the unit a helper body is
// assembled in. Within one, every `__raw_string`'s pointer must be a bare name
// the same function binds with `__raw_alloc`.
var rawStringCallRe = regexp.MustCompile(`__raw_string\(`)

// rawAllocBindRe matches the binding form the helpers use: `var p: i32 =
// __raw_alloc(...)`. The type is always i32 — a raw pointer's surface type in
// these bodies (see TestAsmcoreAddressesAvoidI32Arithmetic).
var rawAllocBindRe = regexp.MustCompile(`var\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*i32\s*=\s*__raw_alloc\(`)

// genFnRe matches the start of one asmcore.fern generator function.
var genFnRe = regexp.MustCompile(`(?m)^(?:pub )?function\s+[A-Za-z_][A-Za-z0-9_]*\s*\(`)

func TestRawStringPointerComesFromRawAlloc(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	path := filepath.Join(root, "examples", "self_host", "asmcore.fern")
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
			if strings.HasPrefix(name, "__raw_addr(") {
				t.Errorf("asmcore.fern:%d: __raw_string's pointer is %s — an INTERIOR "+
					"address. __raw_alloc reserves the 24-byte string-box header ahead "+
					"of the pointer it returns, and only that pointer; boxing an offset "+
					"one stamps the box over the buffer's own bytes (#7351)",
					lineOf(src, chunk.off+open), name)
				continue
			}
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

// splitGeneratorFns cuts src at each top-level generator function. The helper
// body a set of intrinsic calls belongs to is assembled inside one of them, so
// it is the right scope for "this name was allocated here".
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
