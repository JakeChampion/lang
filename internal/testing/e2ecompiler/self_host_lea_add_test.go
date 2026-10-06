package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// On x86-64 an add, or a subtract of a constant, whose result lands in a
// register its left operand does not hold is one lea rather than a copy and
// the two-address op. mix is util.hash_bucket's loop, where the accumulator
// is read after the loop and so cannot share the product's register; edges
// subtracts the least 32-bit immediate, whose negation is no displacement.
const leaAddProg = `@noinline function mix(s: string): i64 {
    let a: i64 = 208357i64;
    let i: i32 = 0;
    while (i < s.len()) {
        a = a * 31i64 + (s[i] as i64);
        i = i + 1;
    }
    return a * 31i64 + (s.len() as i64);
}

@noinline function edges(x: i64, y: i64): i64 {
    let p: i64 = x + y;
    let q: i64 = x - 7i64;
    let r: i64 = y - -2147483648i64;
    let t: i64 = x + 2147483647i64;
    return p * q + r * 3i64 + t + x + y;
}

function main(): i32 {
    if (mix("lea-add") != %dI64) { return 1; }
    if (edges(1000i64, -5i64) != %dI64) { return 2; }
    return 42;
}
`

// copyThenArith is a copy into a register that the next instruction adds to
// or subtracts a constant from: the pair a lea replaces. leaPair and leaImm
// are the two leas that replace it; a frame register as the base is the
// frame's own lea, not one of them.
var (
	copyThenArith = regexp.MustCompile(`movq (%r\w+), (%r\w+)\n\s+(?:addq (?:%r\w+|\$-?\d+)|subq \$-?\d+), (%r\w+)\n`)
	leaPair       = regexp.MustCompile(`leaq \((%r\w+),%r\w+\), %r\w+\n`)
	leaImm        = regexp.MustCompile(`leaq -?\d+\((%r\w+)\), %r\w+\n`)
)

func leaAddExpected() (int64, int64) {
	a := int64(208357)
	s := "lea-add"
	for i := 0; i < len(s); i++ {
		a = a*31 + int64(s[i])
	}
	mix := a*31 + int64(len(s))
	x, y := int64(1000), int64(-5)
	edges := (x+y)*(x-7) + (y+2147483648)*3 + (x + 2147483647) + x + y
	return mix, edges
}

func TestSelfHostLeaAdd(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	mix, edges := leaAddExpected()
	prog := strings.ReplaceAll(fmt.Sprintf(leaAddProg, mix, edges), "I64", "i64")
	src := filepath.Join(dir, "lea.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "lea.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	pairs, imms := 0, 0
	for _, fn := range []string{"mix", "edges"} {
		body := selfHostFnBody(t, raw, fn)
		for _, m := range copyThenArith.FindAllStringSubmatch(body, -1) {
			// Subtracting the least immediate keeps the two-address form.
			if m[2] == m[3] && !strings.Contains(m[0], "subq $-2147483648,") {
				t.Errorf("%s copies and then adds where one lea would do:\n%s", fn, m[0])
			}
		}
		for _, m := range leaPair.FindAllStringSubmatch(body, -1) {
			if m[1] != "%rbp" && m[1] != "%rsp" {
				pairs++
			}
		}
		for _, m := range leaImm.FindAllStringSubmatch(body, -1) {
			if m[1] != "%rbp" && m[1] != "%rsp" {
				imms++
			}
		}
	}
	if pairs == 0 || imms == 0 {
		t.Errorf("mix and edges compute %d adds as a register-pair lea and %d as an immediate lea, want both forms", pairs, imms)
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
