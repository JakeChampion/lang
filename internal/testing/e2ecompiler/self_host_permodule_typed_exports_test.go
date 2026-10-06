package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSelfHostPerModuleTypedHelpersLink: a typed body calls runtime helpers an
// AST-lowered one never did (the packed-byte twins, a dead string view's
// release, the raw floor's free, the map hash seed). Only the entry unit
// defines them, so a library unit's call resolves only if the entry exports
// them through emit_runtime_globls. They were missing from that list, and the
// per-module whole-compiler build stopped linking when its units moved onto the
// typed lowering.
func TestSelfHostPerModuleTypedHelpersLink(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "typedexportsdriver")

	proj := t.TempDir()
	mustWrite(t, proj, "leaf.fern", `import "core/map";

function (s: string) tail(n: i32): str { return slice_unchecked(s, n, s.len()); }

@noinline pub function score(words: string[]): i32 {
    let bytes: u8[] = [];
    for w in words { bytes = bytes.append((w.len() + 96) as u8); }
    let head: [u8] = bytes[0:2];
    let text: string = string_from_bytes_unchecked(bytes);
    let seen: Map[string, i32] = map_new(4);
    let t: i32 = 0;
    for w in words {
        seen = seen.insert(w, 1);
        t = t + w.tail(1).len();
    }
    return head.len() + text.len() + seen.get_or("ab", 0) + t;
}
`)
	mustWrite(t, proj, "main.fern", `import "./leaf";

function main(): i32 { return leaf.score(["ab", "cb", "abc"]); }
`)
	copyStdlibTree(t, proj)
	entry := filepath.Join(proj, "main.fern")
	helpers := []string{
		"__fern_alloc_bytes", "__fern_arr_push_u8", "__fern_arr_push_owned_u8",
		"__fn___fern_arr_slice_u8", "__fn___fern_string_from_bytes_u8",
		"__fn___fern_str_view_free", "__fern_raw_free", "__fern_map_hash_seed",
	}

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
			// Without these the fixture no longer reaches the helpers from a
			// library unit, and cannot catch a missing export.
			for _, h := range helpers {
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
			if code := cmd.ProcessState.ExitCode(); code != 10 {
				t.Errorf("per-module %s program exited %d, want 10", target, code)
			}
		})
	}
}
