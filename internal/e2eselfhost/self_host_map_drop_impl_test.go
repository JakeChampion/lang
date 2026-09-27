package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// core/map's __map_drop_impl frees a handle through __fern_box_free, which
// the self-host lowers as the raw free of the block 8 bytes before the handle
// (#9608). The proof it frees the RIGHT block at the right size is that the
// next 16-byte allocation gets it back: a wrong base or size puts the block in
// another size class, or nowhere, and the fresh handle lands elsewhere.
//
// The handle is built the way map_new_impl lays it out (rc 1 at base, the buf
// word at base + 8) but with no buffer, so only the handle is released.
const mapDropImplReuseSrc = `import "core/map";
function handle(): usize {
    var base: usize = __alloc(16);
    __store_i32(base, 1);
    var h: usize = base + 8;
    __store_ptr(h, 0 as usize);
    return h;
}
function main(): i32 {
    var i: i32 = 0;
    var last: usize = 0 as usize;
    while (i < 1000) {
        var h: usize = handle();
        __map_drop_impl(h);
        last = h;
        i = i + 1;
    }
    if (handle() == last) { return 0; }
    return 3;
}
`

// mapNewImplReuseSrc is the same proof over a real map: map_new_impl with a
// string key tag, so the typed lowering has to lower its __map_hash_seed
// draw, and the drop frees the kv buffer before the handle.
const mapNewImplReuseSrc = `import "core/map";
function main(): i32 {
    var i: i32 = 0;
    var last: usize = 0 as usize;
    while (i < 1000) {
        var h: usize = map_new_impl(4, 1, 0);
        __map_drop_impl(h);
        last = h;
        i = i + 1;
    }
    if (map_new_impl(4, 1, 0) == last) { return 0; }
    return 3;
}
`

// mapSetGetSrc runs core/map's own insert, lookup, len and drop on a
// scalar map. The typed lowering produces them only once it has a contract
// and a lowering for every runtime helper they call by name, including the
// string and array releases a scalar map never reaches at run time.
const mapSetGetSrc = `import "core/map";
function main(): i32 {
    var h: usize = map_new_impl(4, 0, 0);
    var i: i32 = 0;
    while (i < 200) {
        h = __map_set_impl(h, (i * 3) as usize, (i * 7) as usize);
        i = i + 1;
    }
    var s: i32 = __map_len_impl(h);
    i = 0;
    while (i < 200) {
        s = s + (__map_get_or_impl(h, (i * 3) as usize, 0 as usize) as i32);
        i = i + 1;
    }
    __map_drop_impl(h);
    if (s == 139500) { return 0; }
    return 3;
}
`

// mapColumnsSrc reads a map's key, value and boolean columns through
// core/map's snapshot builders. They build a typed array rather than writing
// an array header by hand, so the self-host gets an array in its own layout
// (header and 8-byte slots) rather than native's (#9608).
const mapColumnsSrc = `import "core/map";
function main(): i32 {
    var h: usize = map_new_impl(4, 0, 0);
    var i: i32 = 0;
    while (i < 20) {
        h = __map_set_impl(h, (i * 3) as usize, (i * 7) as usize);
        i = i + 1;
    }
    var ks: i32[] = __map_i32_column(h, 0);
    var vs: i32[] = __map_i32_column(h, __ptr_width());
    var bs: boolean[] = __map_bool_column(h, __ptr_width());
    var s: i32 = ks.len() + vs.len() + bs.len();
    i = 0;
    while (i < ks.len()) {
        s = s + ks[i] + vs[i];
        if (bs[i]) { s = s + 1; }
        i = i + 1;
    }
    __map_drop_impl(h);
    if (s == 1979) { return 0; }
    return 3;
}
`

func TestSelfHostRunsCoreMapFunctions(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	for _, c := range []struct{ name, src string }{
		{"handle", mapDropImplReuseSrc},
		{"map_new_impl", mapNewImplReuseSrc},
		{"set_get", mapSetGetSrc},
		{"columns", mapColumnsSrc},
	} {
		t.Run(c.name, func(t *testing.T) {
			caseDir := t.TempDir()
			srcPath := filepath.Join(caseDir, "main.fern")
			if err := os.WriteFile(srcPath, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			binPath := filepath.Join(caseDir, "prog")
			if out, err := exec.Command(buildFernCLIBin(t), "-target", "x86-64-linux", "-o", binPath, srcPath, stdlibRoot).CombinedOutput(); err != nil {
				t.Fatalf("native build: %v\n%s", err, out)
			}
			run := exec.Command(binPath)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Fatalf("native: exit %d, want 0", code)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					exit, stderr := selfHostCLIRun(t, selfHostBin, stdlibRoot, c.src, target)
					if exit != 0 {
						t.Fatalf("self-host: exit %d, want 0\n%s", exit, stderr)
					}
				})
			}
		})
	}
}
