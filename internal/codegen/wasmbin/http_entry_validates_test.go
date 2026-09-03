package wasmbin

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The wasi:http wrapper's helper closure.
//
// __http_entry is added to the helper set AFTER scanRuntimeHelpers has
// already closed it, so the callees it needs cannot ride in on
// scanRuntimeHelpers' own pass — wasmbin.go closes the set a second
// time over everything the post-scan adds pulled in.
//
// Without that second close the failure is silent at build time and
// remote at run time: a name missing from the set reads 0 out of
// helperIdxs, the wrapper emits `call 0`, and that lands on whatever
// occupies funcidx 0 with the wrong arity. TestBuildHttpHandlerCompiles
// cannot see it — Build returns a well-formed module and the export is
// present; only a validator reading the wrapper's stack discipline
// catches it ("expected i32 but nothing on stack").
//
// So this is the same regression shape as TestTempDirOnlyProgramIsValidWasm,
// one layer up: build the module the wrapper is emitted into, and
// validate it.
func TestHttpHandlerModuleIsValidWasm(t *testing.T) {
	wasmTools, err := exec.LookPath("wasm-tools")
	if err != nil {
		// Not a skip: the wasm toolchain is a checked-in dependency
		// of this repo's test lanes (docs/LOCAL-DEV-LOOP.md).
		t.Fatalf("wasm-tools not on PATH: %v", err)
	}
	src := `
import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok(req.body_string());
}
`
	prog, info := loadAndCheckModule(t, src)
	bin, err := BuildWithOptions(prog, info, BuildOptions{
		ForceMemorySection: true,
		HttpHandler:        true,
	})
	if err != nil {
		t.Fatalf("Build (HttpHandler): %v", err)
	}
	p := filepath.Join(t.TempDir(), "handler.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command(wasmTools, "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("an http-handler module must be valid wasm: %v\n%s", err, out)
	}
}
