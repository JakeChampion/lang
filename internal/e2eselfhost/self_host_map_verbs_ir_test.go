package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapVerbsIR pins generic map-method monomorphisation on the
// self-host IR path. core/map's verbs (`merge` / `extend` / `get_or_insert`)
// are unbounded `[K, V]` receiver methods, which monomorphize_module skips
// (receiver methods) and the erasure path leaves generic (Map is a built-in,
// so promotion excludes it). The shared erased body then reads the key-kind off
// the unsubstituted `Map[K, V]` — where map_key_kind_of defaults to string —
// so an i32-keyed map's integer key was handed to the string path and
// dereferenced as a pointer (SIGSEGV).
//
// register_map_method_generics folds each such method into a free generic
// `__mapm_<verb>[K, V]` (receiver as arg0, the receiver's type-vars as the
// clone's type params) so the proven free-generic worklist clones one concrete
// `__mapm_verb__<K>__<V>` per key/value pair, with subst_ty rewriting
// `Map[K,V] → Map[i32,i32]` — so the cloned body's key-kind is correct.
// mono_expr rewrites `m.verb(args)` → `__mapm_verb(m, args)`, and the map-op
// receiver resolution recovers a Map return type from the call so a chained
// `.get_or` on the result dispatches as a map op (not `i32.get_or`).
//
// Asserts the program decides IR, the asm carries the i32 clone
// (`__mapm_merge__i32__i32`), and the program runs to exit 0 — at i32 keys
// (the case that previously SIGSEGV'd) AND at string keys (the case that was
// accidentally correct under the string default).
//
// Native only: the file-loading driver reads stdlib modules by host path from
// argv (mirrors TestSelfHostStdTestE2E / TestSelfHostImportAliasIR).
func TestSelfHostMapVerbsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := []struct {
		name      string
		src       string
		wantClone string
	}{
		{
			// i32 keys — the case that SIGSEGV'd before the fold (the integer
			// key dereferenced as a string pointer by the default key-kind).
			name:      "i32_keys",
			wantClone: "__mapm_merge__i32__i32",
			src: `import "core/map";
function main(): i32 {
    var a: Map[i32, i32] = map_new(4);
    a = a.insert(1, 10);
    a = a.insert(2, 20);
    var b: Map[i32, i32] = map_new(4);
    b = b.insert(2, 99);
    b = b.insert(3, 30);
    var m: Map[i32, i32] = a.merge(b);
    if (m.len() != 3) { return 1; }
    if (m.get_or(1, 0) != 10) { return 2; }
    if (m.get_or(2, 0) != 99) { return 3; }
    var e: Map[i32, i32] = a.extend(b);
    if (e.get_or(3, 0) != 30) { return 4; }
    var r: (Map[i32, i32], i32) = m.get_or_insert(5, 50);
    if (r.1 != 50) { return 5; }
    if (r.0.get_or(5, 0) != 50) { return 6; }
    // chained map op directly on the merge result (no intermediate var)
    if (a.merge(b).get_or(3, 0) != 30) { return 7; }
    return 0;
}`,
		},
		{
			// string keys — already correct under the string default, but must
			// still ride the fold (a distinct clone, not the i32 one).
			name:      "string_keys",
			wantClone: "__mapm_merge__string__i32",
			src: `import "core/map";
function main(): i32 {
    var a: Map[string, i32] = map_new(4);
    a = a.insert("x", 1);
    var b: Map[string, i32] = map_new(4);
    b = b.insert("y", 2);
    var m: Map[string, i32] = a.merge(b);
    if (m.get_or("x", 0) != 1) { return 1; }
    if (m.get_or("y", 0) != 2) { return 2; }
    return 0;
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := filepath.Join(dir, "mapverbs_"+tc.name+".fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write program: %v", err)
			}
			if got := strings.TrimSpace(runDriverDecide(t, mmc, prog, stdlibRoot)); got != "ir" {
				t.Fatalf("map-verbs program routed %q, want \"ir\" (generic map methods must monomorphise)", got)
			}
			asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("self-host compile failed: %v", err)
			}
			if !strings.Contains(string(asm), tc.wantClone) {
				t.Fatalf("asm missing the monomorphised clone %q (the fold didn't fire)", tc.wantClone)
			}
			bin := buildBin(t, gcc, dir, "mapverbs_"+tc.name, string(asm))
			rc := exec.Command(bin)
			_ = rc.Run()
			if code := rc.ProcessState.ExitCode(); code != 0 {
				t.Errorf("map-verbs IR program exited %d, want 0 (generic map method miscompiled)", code)
			}
		})
	}
}
