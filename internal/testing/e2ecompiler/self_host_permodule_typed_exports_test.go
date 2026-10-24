package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestSelfHostPerModuleTypedHelpersLink(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "typedexportsdriver")

	const viewLeaf = `import "core/map";

function (s: string) tail(n: i32): str { return slice_unchecked(s, n, s.len()); }

@noinline pub function score(words: string[]): i32 {
    let bytes: u8[] = [];
    for w in words { bytes = bytes.append((w.len() + 96) as u8); }
    // Two slices joined at a phi outlive an instruction, so they take the
    // heap descriptor (__sem_byte_view_slice) rather than a frame one.
    let head: [u8] = bytes[0:2];
    if (words.len() > 2) {
        head = bytes[1:3];
    }
    let text: string = string_from_bytes_unchecked(bytes);
    let seen: Map[string, i32] = map_new(4);
    let t: i32 = 0;
    for w in words {
        seen = seen.insert(w, 1);
        t = t + w.tail(1).len();
    }
    return head.len() + text.len() + seen.get_or("ab", 0) + t;
}
`
	const viewMain = `import "./leaf";

function main(): i32 { return leaf.score(["ab", "cb", "abc"]); }
`
	helpers := []string{
		"__fern_alloc_bytes", "__fern_arr_push_u8", "__fern_arr_push_owned_u8",
		"__fn___sem_byte_view_slice", "__fn___fern_string_from_bytes_u8",
		"__fn___fern_str_view_free", "__fern_raw_free", "__fern_map_hash_seed",
	}
	const copyLeaf = `@noinline pub function replace(own a: u8[]): u8[] {
    return a.with(0, 99 as u8);
}
`
	const copyMain = `import "./leaf";
function main(): i32 {
    let a: u8[] = [];
    for word in args() { a = a.append((word.len() % 10) as u8); }
    let first: u8 = a[0];
    let saved = a;
    let changed = leaf.replace(a);
    if (saved[0] != first || changed[0] != 99 as u8) { return 21; }
    if (saved.len() != changed.len()) { return 22; }
    let unique = leaf.replace(changed);
    if (unique[0] != 99 as u8 || unique.len() != saved.len()) { return 23; }
    return 0;
}
`
	for _, tc := range []struct {
		name, leaf, main string
		helpers          []string
		exit             int
	}{
		{"byte-view", viewLeaf, viewMain, helpers, 10},
		{"byte-copy", copyLeaf, copyMain, []string{"__fn___fern_arr_slice_u8"}, 0},
		{"isatty", "@noinline pub function tty(): i32 { if (isatty(1)) { return 1; } return 0; }\n", "import \"./leaf\";\nfunction main(): i32 { return leaf.tty() + 7; }\n", []string{"__fn___fern_isatty"}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWrite(t, proj, "leaf.fern", tc.leaf)
			mustWrite(t, proj, "main.fern", tc.main)
			copyStdlibTree(t, proj)
			entry := filepath.Join(proj, "main.fern")

			for _, target := range []string{"x86-64-linux", "arm64-linux"} {
				t.Run(target, func(t *testing.T) {
					gcc := x86gcc
					var qemu string
					if target == "arm64-linux" {
						gcc, qemu = arm64Tooling(t)
					}
					outDir := filepath.Join(proj, target)
					if err := os.MkdirAll(outDir, 0o755); err != nil {
						t.Fatal(err)
					}
					if out, err := runX86_64Bin(x86runner, driverBin, entry, "-target", target, "-per-module-emit-all", "-out-dir", outDir).CombinedOutput(); err != nil {
						t.Fatalf("emit-all: %v\n%s", err, out)
					}
					ents, err := os.ReadDir(outDir)
					if err != nil {
						t.Fatal(err)
					}
					var objs []string
					library := ""
					for _, e := range ents {
						p := filepath.Join(outDir, e.Name())
						objs = append(objs, p)
						b, err := os.ReadFile(p)
						if err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(string(b), ".globl _start") {
							library += string(b)
						}
					}
					sort.Strings(objs)
					for _, h := range tc.helpers {
						if !strings.Contains(library, h) {
							t.Errorf("no library unit references %s", h)
						}
					}
					bin := filepath.Join(proj, target+"_prog")
					linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
					if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
						t.Fatalf("per-module link failed — a library unit's helper has no exported definer: %v\n%s", err, lout)
					}
					var cmd *exec.Cmd
					if target == "arm64-linux" {
						cmd = runArm64Bin(qemu, bin)
					} else {
						cmd = runX86_64Bin(x86runner, bin)
					}
					_ = cmd.Run()
					if code := cmd.ProcessState.ExitCode(); code != tc.exit {
						t.Errorf("per-module %s program exited %d, want %d", target, code, tc.exit)
					}
				})
			}
		})
	}
}
