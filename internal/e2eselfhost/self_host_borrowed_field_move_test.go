package e2eselfhost

import (
	"strings"
	"testing"
)

// A local bound from a struct field read holds no reference of its own on the
// AST lowering: the bind takes none and the exit sweep releases none. Storing
// it into a construction at its last use is therefore not a move. When it was
// taken as one, the construction skipped its retain, and the `own` receiver's
// drop freed the field under the new struct (#10475). The per-module-built
// compiler crashed on exactly this shape in irlower's own strarr_own_node.
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
