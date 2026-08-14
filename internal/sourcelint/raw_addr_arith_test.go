package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A raw pointer's surface type in the runtime-helper sources is `i32`, so a
// `p + off` written there is an i32 add: arm64 sign-extends the sum back to 32
// bits (`sxtw x0, w0`) and any address above 4 GiB arrives truncated. Every
// Linux target puts the image low enough that the narrowing is a no-op, so the
// only place it shows is arm64-darwin, where `__PAGEZERO` forces the base to
// `0x100000000` — the address lands in unmapped memory and the access faults
// (#6386: `__load_i32(buf + 4)` on `__raw_scratch`'s buffer).
//
// The address argument therefore has to be a bare pointer or `__raw_addr(p,
// off)`, which does the add at full pointer width. The two-argument load/stores
// fold their own `off` into the addressing mode; the ONE-argument
// `__load_i32` / `__load_i64` / `__load_ptr` have no offset operand, which is
// how #6386 happened.
//
// Only asmcore.fern is scanned: it is the sole place raw addresses are
// i32-typed. The stdlib's raw-memory code (internal/stdlib/core/map.fern) types
// its pointers `usize`, so its `buf + 4` is already full-width arithmetic.
var addrTakingIntrinsics = []string{
	"__load_i32",
	"__load_i64",
	"__load_ptr",
	"__raw_load8",
	"__raw_load_ptr",
	"__raw_store8",
	"__raw_store_ptr",
	"__raw_string",
	"__raw_array",
	"__raw_addr",
}

// bareAddrArgRe matches a plain pointer name, one of the two safe spellings of
// an address argument (the other being a `__raw_addr(...)` call).
//
// asmcore.fern builds helper bodies as Fern string literals, so a name can also
// arrive as the generator's own interpolation — `__raw_store8(" + buf + ", 0,
// 2)` in sockaddr_head. That is still a bare name in the emitted text, so the
// leading `" + ` and trailing ` + "` seams are part of the accepted form. They
// have to sit at the very ends: `buf + " + statoff(t, "mode") + "` (the #6386
// shape) keeps the seam in the middle and is rejected.
var bareAddrArgRe = regexp.MustCompile(`^(?:"\s*\+\s*)?[A-Za-z_][A-Za-z0-9_]*(?:\s*\+\s*")?$`)

func TestAsmcoreAddressesAvoidI32Arithmetic(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	path := filepath.Join(root, "examples", "self_host", "asmcore.fern")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read asmcore.fern: %v", err)
	}
	src := string(raw)

	for _, name := range addrTakingIntrinsics {
		for _, open := range callOpenParens(src, name) {
			arg, ok := firstArg(src, open)
			if !ok {
				t.Errorf("asmcore.fern:%d: unbalanced parentheses after %s(", lineOf(src, open), name)
				continue
			}
			arg = strings.TrimSpace(arg)
			if strings.HasPrefix(arg, "__raw_addr(") || bareAddrArgRe.MatchString(arg) {
				continue
			}
			t.Errorf("asmcore.fern:%d: %s's address argument is `%s` — i32 arithmetic on a raw "+
				"address truncates above 4 GiB (arm64-darwin faults, every Linux target does not); "+
				"use __raw_addr(ptr, off), which adds at full pointer width (#6386)",
				lineOf(src, open), name, arg)
		}
	}
}

// callOpenParens returns the offset of the `(` of every `name(` occurrence that
// is a whole identifier (so `__load_i32` does not also match a longer name).
func callOpenParens(src, name string) []int {
	var out []int
	for i := 0; ; {
		j := strings.Index(src[i:], name+"(")
		if j < 0 {
			return out
		}
		at := i + j
		if at == 0 || !isIdentByte(src[at-1]) {
			out = append(out, at+len(name))
		}
		i = at + len(name)
	}
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// firstArg returns the text of the first argument of the call whose `(` is at
// open — everything up to the matching `)` or the first comma at depth 1.
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
