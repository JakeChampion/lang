package e2eselfhost

import (
	"strings"
	"testing"
)

// A fresh-ret struct local bound from a borrowed parameter carries that
// parameter's field buffers uncounted: `var st: Buf = se.add_local(1)` copies
// se.ops into st's box. Its first rebind replaced ops and released the old one
// — se.ops, the CALLER's buffer — because the rebind release compared the
// superseded box against the new value only. The binding now snapshots the box
// it was derived from (seed_snapshot_local) and a field still shared with it is
// left alone. Pinned under the sanitizer, whose quarantine is what turns the
// stale free into a finding rather than a value that happens to still be there.
const snapshotLocalSourceSrc = `struct Buf { ops: i32[], locals: i32[], n: i32 }

function (s: Buf) emit(v: i32): Buf { return Buf { ...s, ops: s.ops.append(v) }; }
function (s: Buf) add_local(v: i32): Buf { return Buf { ...s, locals: s.locals.append(v) }; }

function step(se: Buf): Buf {
    var st: Buf = se.add_local(1);
    st = st.emit(2);
    st = st.emit(3);
    return st;
}

function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var b: Buf = Buf { ops: [1, 2, 3], locals: [], n: 0 };
        var r: Buf = step(b);
        var k: i32 = 0;
        while (k < b.ops.len()) { total = total + b.ops[k]; k = k + 1; }
        total = total + r.ops.len() + r.locals.len();
        i = i + 1;
    }
    return total % 64;
}`

func TestSelfHostSnapshotLocalKeepsSourceFieldsX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "snap_src", snapshotLocalSourceSrc, []string{"FERN_SANITIZE=1"})
	stderr, code := hevRun(t, runner, bin)
	if code != 24 {
		t.Errorf("exit=%d, want 24 (600 %% 64: the caller's buffer read back intact after every call)", code)
	}
	if strings.Contains(stderr, "fern-sanitizer: use-after-free") {
		t.Errorf("the first rebind of the derived local released the caller's field buffer: %q", stderr)
	}
}
