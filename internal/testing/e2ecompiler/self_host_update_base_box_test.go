package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// space reads its receiver apart and rebuilds it on two arms, so the pass
// that hands a record of scalars to a callee as its fields would split the
// receiver. The receiver is the base of a record update, so it keeps its
// box, the inference counts it, and the update reuses the box in place: the
// loop allocates nothing.
const updateBaseBoxProgram = `import "std/bench";

struct Cfg { tabify: boolean, sep: u8[] }
struct Line { buf: usize, pos: i32, pend: i32 }

@noinline function (l: Line) space(c: Cfg): Line {
    if (c.tabify) {
        return Line { ...l, pos: l.pos + 1, pend: l.pend + 1 };
    }
    buf_push_byte(l.buf, 32);
    return Line { ...l, pos: l.pos + 1 };
}

@noinline function put_sep(l0: Line, c: Cfg): Line {
    let l: Line = l0;
    for _ in 0..c.sep.len() {
        l = l.space(c);
    }
    return l;
}

function main(): i32 {
    let b: usize = buf_new(64);
    let c: Cfg = Cfg { tabify: false, sep: [32 as u8, 32 as u8] };
    let t: Cfg = Cfg { tabify: true, sep: [32 as u8] };
    let l: Line = Line { buf: b, pos: 0, pend: 0 };
    let a0: i64 = bench.alloc_count();
    for _ in 0..1000 {
        l = put_sep(l, c);
        l = put_sep(l, t);
        buf_clear(b);
    }
    let allocated: i64 = bench.alloc_count() - a0;
    buf_free(b);
    print(l.pos.to_string() + " " + l.pend.to_string() + " " + allocated.to_string());
    if (l.pos != 3000 || l.pend != 1000) {
        return 1;
    }
    if (allocated != 0) {
        return 2;
    }
    if (__rc_underflow_count() != 0) {
        return 3;
    }
    return 0;
}
`

func TestSelfHostUpdateBaseKeepsItsBox(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "update_base.fern")
			if err := os.WriteFile(src, []byte(updateBaseBoxProgram), 0o644); err != nil {
				t.Fatal(err)
			}
			stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
			if code != 0 {
				t.Errorf("exit %d, want 0 (1: a wrong value, 2: the loop allocated, 3: a count went below zero)\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "live_bytes=0") {
				t.Errorf("%q, want every block freed", stderr)
			}
		})
	}
}
