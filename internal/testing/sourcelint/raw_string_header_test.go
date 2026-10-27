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
	paths, err := filepath.Glob(filepath.Join(root, "compiler", "runtime", "*", "*.fern"))
	if err != nil {
		t.Fatal(err)
	}
	coverageAllocator, err := os.ReadFile(filepath.Join(root, "compiler", "runtime", "templates", "__fern_cov_table.fern"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := blankFullLineComments(string(raw))
		for _, chunk := range splitGeneratorFns(src) {
			allocated := map[string]bool{}
			for _, m := range rawAllocBindRe.FindAllStringSubmatch(chunk.text, -1) {
				allocated[m[1]] = true
			}
			for _, loc := range rawStringCallRe.FindAllStringIndex(chunk.text, -1) {
				calls++
				open := loc[1] - 1
				arg, ok := firstArg(chunk.text, open)
				if !ok {
					t.Errorf("%s:%d: unbalanced parentheses after __raw_string(",
						path, lineOf(src, chunk.off+open))
					continue
				}
				name := strings.TrimSpace(arg)
				if !bareAddrArgRe.MatchString(name) {
					t.Errorf("%s:%d: __raw_string's pointer is `%s` — it has to be "+
						"the bare name of a __raw_alloc result, whose reserved 24-byte header "+
						"the box is stamped into (#7351)", path, lineOf(src, chunk.off+open), name)
					continue
				}
				bare := strings.Trim(strings.TrimSpace(strings.Trim(name, `"`)), "+ ")
				if !allocated[bare] && !coverageStringTransfer(chunk, bare, loc[0], string(coverageAllocator)) {
					t.Errorf("%s:%d: __raw_string boxes %q, which %s does not bind "+
						"with __raw_alloc. __raw_alloc is the ONLY producer that reserves the "+
						"24-byte string-box header ahead of its pointer; boxing a scratch "+
						"buffer, an argv entry or a borrowed address writes the box over 24 "+
						"bytes that belong to something else, and nothing reports it at run "+
						"time (#7351)", path, lineOf(src, chunk.off+open), bare, chunk.name)
				}
			}
		}
	}
	if calls == 0 {
		t.Fatal("no raw string constructors in the runtime modules")
	}
}

var coverageLoadRe = regexp.MustCompile(`let\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*usize\s*=\s*__raw_load_ptr\(\s*__raw_cover\(\)\s*,\s*0\s*\)\s*;`)

// The coverage slot owns a pointer from __fern_cov_table.
// The report clears that slot before it transfers the allocation to a string.
func coverageStringTransfer(chunk genFnChunk, name string, call int, allocator string) bool {
	if chunk.name != "__fern_cov_report" || call < 0 || call > len(chunk.text) {
		return false
	}
	before := blankFullLineComments(chunk.text[:call])
	loads := coverageLoadRe.FindAllStringSubmatchIndex(before, -1)
	if len(loads) != 1 || before[loads[0][2]:loads[0][3]] != name {
		return false
	}
	identifier := regexp.QuoteMeta(name)
	rebound := regexp.MustCompile(`(?m)(?:^\s*|\blet\s+)` + identifier + `\s*(?::[^;=\n]+)?(?:(?:[+\-*/%&|^]|<<|>>)?=|\+\+|--)`)
	if rebound.MatchString(before[loads[0][1]:]) {
		return false
	}
	handoff := regexp.MustCompile(`(?s)if\s*\(\s*` + identifier + `\s*!=\s*0\s+as\s+usize\s*\)\s*\{\s*__raw_store_ptr\(\s*__raw_cover\(\)\s*,\s*0\s*,\s*0\s+as\s+usize\s*\)\s*;\s*let\s+[A-Za-z_][A-Za-z0-9_]*\s*:\s*string\s*=\s*$`)
	if !handoff.MatchString(before) {
		return false
	}
	for _, producer := range splitGeneratorFns(blankFullLineComments(allocator)) {
		if producer.name != "__fern_cov_table" {
			continue
		}
		for _, allocation := range rawAllocBindRe.FindAllStringSubmatchIndex(producer.text, -1) {
			pointer := regexp.QuoteMeta(producer.text[allocation[2]:allocation[3]])
			allocationEnd := strings.IndexByte(producer.text[allocation[1]:], ';')
			if allocationEnd < 0 {
				continue
			}
			tail := producer.text[allocation[1]+allocationEnd+1:]
			store := regexp.MustCompile(`__raw_store_ptr\(\s*__raw_cover\(\)\s*,\s*0\s*,\s*` + pointer + `\s*\)\s*;`).FindStringIndex(tail)
			returned := regexp.MustCompile(`\breturn\s+` + pointer + `\s*;`).FindStringIndex(tail)
			assigned := regexp.MustCompile(`(?m)(?:^\s*|\blet\s+)` + pointer + `\s*(?::[^;=\n]+)?(?:(?:[+\-*/%&|^]|<<|>>)?=|\+\+|--)`)
			if store != nil && returned != nil && returned[0] > store[1] && !assigned.MatchString(tail) && len(regexp.MustCompile(`\breturn\b`).FindAllStringIndex(tail, -1)) == 1 {
				return true
			}
		}
	}
	return false
}

func TestCoverageStringTransfer(t *testing.T) {
	const producer = `function __fern_cov_table(): usize {
  let t: usize = __raw_alloc(__FERN_COVER_COUNT * 8 + 8);
  __raw_store_ptr(__raw_cover(), 0, t);
  return t;
}`
	const report = `function __fern_cov_report(): void {
  let t: usize = __raw_load_ptr(__raw_cover(), 0);
  if (t != 0 as usize) {
    __raw_store_ptr(__raw_cover(), 0, 0 as usize);
    let table_own: string = __raw_string(t, __FERN_COVER_COUNT * 8 + 8);
  }
}`
	cases := []struct {
		name, allocator, source, pointer string
		want                             bool
	}{
		{"owned-table-transfer", producer, report, "t", true},
		{"borrowed-allocation", strings.Replace(producer, "__raw_alloc(", "__raw_scratch(", 1), report, "t", false},
		{"unpublished-allocation", strings.Replace(producer, "__raw_store_ptr(__raw_cover(), 0, t);", "", 1), report, "t", false},
		{"wrong-producer-slot", strings.Replace(producer, "__raw_cover(), 0, t", "__raw_cover(), 1, t", 1), report, "t", false},
		{"offset-allocation", strings.Replace(producer, "  __raw_store_ptr", "  t = t + 8;\n  __raw_store_ptr", 1), report, "t", false},
		{"compound-offset", strings.Replace(producer, "  __raw_store_ptr", "  t += 8;\n  __raw_store_ptr", 1), report, "t", false},
		{"incremented-allocation", strings.Replace(producer, "  __raw_store_ptr", "  t++;\n  __raw_store_ptr", 1), report, "t", false},
		{"uncleared-slot", producer, strings.Replace(report, "__raw_store_ptr(__raw_cover(), 0, 0 as usize);", "", 1), "t", false},
		{"wrong-clear-slot", producer, strings.Replace(report, "__raw_cover(), 0, 0 as usize", "__raw_cover(), 1, 0 as usize", 1), "t", false},
		{"missing-null-guard", producer, strings.Replace(report, "if (t != 0 as usize)", "if (true)", 1), "t", false},
		{"reassigned-pointer", producer, strings.Replace(report, "  if (t", "  t = __raw_cover();\n  if (t", 1), "t", false},
		{"compound-reassignment", producer, strings.Replace(report, "  if (t", "  t += 8;\n  if (t", 1), "t", false},
		{"offset-load", producer, strings.Replace(report, "__raw_load_ptr(__raw_cover(), 0);", "__raw_load_ptr(__raw_cover(), 0) + 8;", 1), "t", false},
		{"shadowed-pointer", producer, strings.Replace(report, "  if (t", "  let t: usize = __raw_cover();\n  if (t", 1), "t", false},
		{"foreign-pointer", producer, report, "foreign", false},
		{"different-consumer", producer, strings.Replace(report, "__fern_cov_report", "different", 1), "t", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks := splitGeneratorFns(tc.source)
			if len(chunks) != 1 {
				t.Fatal("expected one report function")
			}
			got := coverageStringTransfer(chunks[0], tc.pointer, strings.Index(tc.source, "__raw_string("), tc.allocator)
			if got != tc.want {
				t.Fatalf("coverage transfer = %t, want %t", got, tc.want)
			}
		})
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
