package x86_64

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/symname"
)

// Every Fern function symbol is mangled, so no Fern identifier can name a
// token the assembler resolves as something else. The escape this replaced
// enumerated those tokens instead, and an enumeration is only ever as current
// as the binutils release it was measured against — `r16`..`r31` became
// registers in 2.42 and silently turned `call r16` into an indirect call.
// Names that would still be dangerous bare are the point of the test.
func TestAsmFnNameMangles(t *testing.T) {
	for _, n := range []string{
		// x86 registers of every file and width, segment / control / debug
		// registers, the Intel-syntax size keywords and expression operators.
		"ch", "al", "ax", "rax", "r15d", "spl", "r16", "r31b",
		"xmm0", "zmm31", "mm0", "k0", "bnd0", "tmm0", "cr0", "dr7",
		"cs", "ds", "es", "ss", "fs", "gs", "st", "rip", "eip",
		"byte", "qword", "zmmword", "offset", "short", "near", "far", "flat",
		"and", "or", "xor", "not", "shl", "shr", "mod", "eq", "ne", "lt", "ge",
		// …and the ordinary names, which are mangled the same way: one rule,
		// so nothing depends on classifying a name correctly.
		"main", "foo", "len", "__method_MapIter_key", "__closure_drop_f",
	} {
		got := AsmFnName(n)
		if got != symname.Prefix+n {
			t.Errorf("AsmFnName(%q) = %q, want %q", n, got, symname.Prefix+n)
		}
		if src, ok := symname.Source(got); !ok || src != n {
			t.Errorf("Source(AsmFnName(%q)) = %q, %v — debug info reads the source name back through this", n, src, ok)
		}
	}
}

// The mangled namespace has to stay disjoint from the runtime helpers'. A
// helper named `__fn_*` would be reachable from a Fern identifier again, and
// the collision would be a duplicate definition or a silently wrong callee —
// the #6022 failure, just with the two namespaces swapped.
func TestRuntimeHelpersAvoidTheFnNamespace(t *testing.T) {
	// A program that pulls in a broad slice of the runtime: allocation,
	// strings, arrays, rc traffic, and a closure.
	asm := compile(t, `function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    var xs: string[] = ["a", "b"];
    xs = xs.append("c" + "d");
    var n: i32 = 1;
    return apply(function (x: i32): i32 { return x + n; }, xs.len()) - 3;
}`)
	for _, line := range strings.Split(asm, "\n") {
		sym, ok := strings.CutPrefix(line, ".globl ")
		if !ok {
			continue
		}
		if src, mangled := symname.Source(sym); mangled && src == "" {
			t.Errorf("emitted a bare %q symbol", symname.Prefix)
		}
	}
	// The runtime helpers this program reaches are emitted under their own
	// names, unprefixed — the definitions the mangled call sites do NOT go
	// through.
	for _, helper := range []string{"__fern_alloc", "__fern_rc_dec", "__fern_str_dec"} {
		if !strings.Contains(asm, "\n.globl "+helper+"\n") {
			t.Errorf("runtime helper %s is not defined under its own name", helper)
		}
	}
}
