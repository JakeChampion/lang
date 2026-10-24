package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var mapKeyedArm64Cases = []struct {
	name string
	src  string
	want int
}{
	{"struct-string-field", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }

function main(): i32 {
    let m: Map[Name, i32] = map_new(8);
    m = m.insert(Name { first: "ada", rank: 1 }, 10);
    m = m.insert(Name { first: "bob", rank: 2 }, 20);
    if (m.get_or(Name { first: "a" + "da", rank: 1 }, -1) != 10) { return 1; }   // map_get
    if (m.get_or(Name { first: "ada", rank: 9 }, -1) != -1) { return 2; }        // absent
    if (!m.contains(Name { first: "b" + "ob", rank: 2 })) { return 3; }               // map_has
    if (m.contains(Name { first: "zzz", rank: 1 })) { return 4; }
    m = m.insert(Name { first: "a" + "da", rank: 1 }, 99);                       // map_set overwrite
    if (m.len() != 2) { return 5; }
    if (m.get_or(Name { first: "ada", rank: 1 }, -1) != 99) { return 6; }
    let gone: boolean = m.contains(Name { first: "a" + "da", rank: 1 });
    let m2 = m.remove(Name { first: "a" + "da", rank: 1 });             // map_delete
    if (!gone) { return 7; }
    if (m2.contains(Name { first: "ada", rank: 1 })) { return 8; }
    if (m2.len() != 1) { return 9; }
    return 42;
}`, 42},

	{"struct-scalars", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct P { a: i32, b: i32 }

function main(): i32 {
    let m: Map[P, i32] = map_new(8);
    m = m.insert(P { a: 1, b: 2 }, 10);
    m = m.insert(P { a: 3, b: 4 }, 20);
    if (m.get_or(P { a: 1, b: 2 }, -1) != 10) { return 1; }
    if (m.get_or(P { a: 3, b: 4 }, -1) != 20) { return 2; }
    if (m.get_or(P { a: 1, b: 4 }, -1) != -1) { return 3; }   // both fields must match
    if (m.get_or(P { a: 9, b: 9 }, -1) != -1) { return 4; }
    return 42;
}`, 42},

	{"enum-key", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B, C(string) }

function main(): i32 {
    let m: Map[Tag, i32] = map_new(8);
    m = m.insert(A(1), 100);
    m = m.insert(B, 200);
    m = m.insert(C("x" + "y"), 300);
    if (m.get_or(A(1), 0) != 100) { return 1; }
    if (m.get_or(B, 0) != 200) { return 2; }
    if (m.get_or(C("xy"), 0) != 300) { return 3; }    // fresh string payload
    if (m.get_or(A(2), -1) != -1) { return 4; }       // same variant, different payload
    if (m.get_or(C("zz"), -1) != -1) { return 5; }
    return 42;
}`, 42},
}

func TestSelfHostMapKeyedArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range mapKeyedArm64Cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "mapkeyed_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			out, err := runX86_64Bin(x86runner, driver, entry, root, "-target", "arm64-linux").Output()
			if err != nil || len(out) == 0 {
				t.Fatalf("%s: arm64 driver failed (%d bytes, err %v)", tc.name, len(out), err)
			}
			if !strings.Contains(string(out), ".Lssa_") {
				t.Fatalf("%s: arm64 asm has no .Lssa_ marker", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "mapkeyed_"+tc.name, string(out))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d (a non-42 value is the failing check's index)", tc.name, code, tc.want)
			}
		})
	}
}
