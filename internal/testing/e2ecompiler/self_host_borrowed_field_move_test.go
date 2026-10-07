package e2ecompiler

import (
	"strings"
	"testing"
)

// A local bound from a struct field read, stored into a construction at its
// last use, is not a move: the construction retains it, since the `own`
// receiver's drop still releases the field. Taken as a move, that drop freed
// the field under the new struct (#10475).
const borrowedFieldMoveSrc = `struct Frame { key: string, n: i32 }
struct Acc { fr: Frame, k: i32 }
function node(own st: Acc): Acc {
    let fr: Frame = st.fr;
    return Acc { fr: fr, k: st.k + 1 };
}
function main(): i32 {
    let f: Frame = Frame { key: "k" + "x", n: 1 };
    let acc: Acc = Acc { fr: f, k: 0 };
    let i: i32 = 0;
    while (i < 5) { acc = node(acc); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc.k + acc.fr.key.len();
}
`

func TestSelfHostBorrowedFieldMoveX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		t.Run(mode, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, borrowedFieldMoveSrc, []string{mode})
			stderr, exit := hevRun(t, runner, buildBin(t, gcc, dir, "bfm", asm))
			if exit != 7 || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit %d, want 7 with no sanitizer finding\n%s", exit, stderr)
			}
			if mode == "FERN_LEAKCHECK=1" {
				assertBalancedCensus(t, stderr)
			}
		})
	}
}
