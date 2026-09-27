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

func TestSelfHostMapDropImplFreesTheHandle(t *testing.T) {
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

	caseDir := t.TempDir()
	srcPath := filepath.Join(caseDir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(mapDropImplReuseSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(caseDir, "prog")
	if out, err := exec.Command(buildFernCLIBin(t), "-target", "x86-64-linux", "-o", binPath, srcPath, stdlibRoot).CombinedOutput(); err != nil {
		t.Fatalf("native build: %v\n%s", err, out)
	}
	run := exec.Command(binPath)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("native: exit %d, want 0 (the freed handle comes back)", code)
	}

	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			exit, stderr := selfHostCLIRun(t, selfHostBin, stdlibRoot, mapDropImplReuseSrc, target)
			if exit != 0 {
				t.Fatalf("self-host: exit %d, want 0 (the freed handle comes back)\n%s", exit, stderr)
			}
		})
	}
}
