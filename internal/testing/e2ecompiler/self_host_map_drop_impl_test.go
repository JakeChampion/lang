package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const mapDropImplReuseSrc = `import "core/map";
function handle(): usize {
    let base: usize = __alloc(16);
    __store_i32(base, 1);
    let h: usize = base + 8;
    __store_ptr(h, 0 as usize);
    return h;
}
function main(): i32 {
    let i: i32 = 0;
    let last: usize = 0 as usize;
    while (i < 1000) {
        let h: usize = handle();
        __map_drop_impl(h);
        last = h;
        i = i + 1;
    }
    if (handle() == last) { return 0; }
    return 3;
}
`

const mapNewImplReuseSrc = `import "core/map";
function main(): i32 {
    let i: i32 = 0;
    let last: usize = 0 as usize;
    while (i < 1000) {
        let h: usize = map_new_impl(4, 1, 0);
        __map_drop_impl(h);
        last = h;
        i = i + 1;
    }
    let again: usize = map_new_impl(4, 1, 0);
    let same: boolean = again == last;
    __map_drop_impl(again);
    if (same) { return 0; }
    return 3;
}
`

const mapSetGetSrc = `import "core/map";
function main(): i32 {
    let h: usize = map_new_impl(4, 0, 0);
    let i: i32 = 0;
    while (i < 200) {
        h = __map_set_impl(h, (i * 3) as usize, (i * 7) as usize);
        i = i + 1;
    }
    let s: i32 = __map_len_impl(h);
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

const mapColumnsSrc = `import "core/map";
function main(): i32 {
    let h: usize = map_new_impl(4, 0, 0);
    let i: i32 = 0;
    while (i < 20) {
        h = __map_set_impl(h, (i * 3) as usize, (i * 7) as usize);
        i = i + 1;
    }
    let ks: i32[] = __map_i32_column(h, 0);
    let vs: i32[] = __map_i32_column(h, __ptr_width());
    let bs: boolean[] = __map_bool_column(h, __ptr_width());
    let s: i32 = ks.len() + vs.len() + bs.len();
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
	stdlibRoot, err := filepath.Abs("../../stdlib")
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
