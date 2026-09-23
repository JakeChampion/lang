package wasmbin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/fernrt"
	"github.com/jakechampion/lang/internal/strerror"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/module"
)

// Inject a canonical-ABI socket Err payload into the production error-return
// sequence. Unknown is discriminant zero, so blindly negating it reports
// success. The conversion itself is the real Fern runtime helper, lowered and
// emitted by the normal backend; no socket permission or port race is needed.
func TestWasiSocketErrorReturns(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	_, fn, err := fernrt.Func("__fern_wasi_socket_errno", 4)
	if err != nil {
		t.Fatal(err)
	}
	body, locals, err := emitBody(fn, &emitCtx{})
	if err != nil {
		t.Fatal(err)
	}
	var probe []byte
	probe = inst.InstI32Const(probe, 4)
	probe = inst.InstLocalGet(probe, 0)
	probe = memory.InstI32Store8(probe, 0, 0)
	probe = emitErrnoNegReturn(probe, 1, map[string]uint32{"__fern_wasi_socket_errno": 1})
	m := module.New()
	m.TypeParams = [][]byte{{encode.ValtypeI32}}
	m.TypeResults = [][]byte{{encode.ValtypeI32}}
	m.FunctionTypeidxs = []uint32{0, 0}
	m.MemoryPresent, m.MemoryMin = true, 1
	m.ExportNames, m.ExportKinds, m.ExportIdxs = []string{"probe"}, []byte{0}, []uint32{0}
	m.CodeBodies = [][]byte{
		inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32), probe),
		inst.PutFunctionBody(nil, locals, body),
	}
	p := filepath.Join(t.TempDir(), "socket-errors.wasm")
	if err := os.WriteFile(p, module.Build(m), 0o644); err != nil {
		t.Fatal(err)
	}
	for code := 0; code <= 255; code++ {
		// All defined host errors and the end of the ABI byte range.
		if code >= len(strerror.WasiSocketErrorCodes) && code != 255 {
			continue
		}
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			cmd := exec.Command(wasmtime, "run", "--invoke", "probe", p, strconv.Itoa(code))
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			got, err := strconv.Atoi(strings.TrimSpace(string(out)))
			want := -strerror.WasiSocketErrno(code)
			if err != nil || got != want || got >= 0 {
				t.Errorf("socket Err(%d) returned %q, want %d", code, out, want)
			}
		})
	}
}
