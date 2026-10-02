package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfHostClassifyDriver prints the lowering kind the self-host composer
// derives for three functions of the proxy world: `outgoing-handler.handle`,
// whose `error-code` result a `use` brings in from wasi:http/types;
// `future-incoming-response.get`, which reads the same variant from inside
// that interface; and `outgoing-request.set-method`, whose only heap data is
// a parameter.
const selfHostClassifyDriver = `
function main(): i32 {
    let tbody: i32[] = wit_section_body(blob_to_bytes(proxy_world_payload()), 7);
    print_int(wit_classify(tbody, "wasi:http/outgoing-handler@0.2.0", "handle"));
    write("\n");
    print_int(wit_classify(tbody, "wasi:http/types@0.2.0", "[method]future-incoming-response.get"));
    write("\n");
    print_int(wit_classify(tbody, "wasi:http/types@0.2.0", "[method]outgoing-request.set-method"));
    write("\n");
    return 0;
}
`

// TestSelfHostClassifyUsedTypeFromAnotherInterface pins the self-host mirror
// of componenttype's TestClassifyUsedTypeFromAnotherInterface: a type a WIT
// `use` brings in from another interface classifies as what it is, so the
// host's `handle` lowers with realloc (2) like `get` (2), and `set-method`
// is a memory lowering (1) for its string parameter.
func TestSelfHostClassifyUsedTypeFromAnotherInterface(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	driverWat := witCompileToWat(t, dir, "classify", withPrintInt(selfHostClassifyDriver))
	out, err := exec.Command(wasmtime, "run", "--dir", dir, driverWat).CombinedOutput()
	if err != nil {
		t.Fatalf("run driver: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "2\n2\n1" {
		t.Fatalf("kinds = %q; want handle 2, get 2, set-method 1 (driver %s)", got, filepath.Base(driverWat))
	}
}
