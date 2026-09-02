package wasmbin

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/inst"
)

// TestArrayReturningHelpersWriteRcHeader pins the cap / rc / len header on
// every runtime helper that hands a Fern array back to compiled code (#7969).
//
// A wasm32 array is a pointer to its elements with capacity at data-12,
// refcount at data-8 and length at data-4. __fern_args wrote only the length
// prefix, so the caller's scope-exit dec read the refcount out of the previous
// allocation's tail; in a driver large enough to leave a zero there,
// `for a in args()` reported one over-release, while the same source built for
// x86-64 was clean. Several siblings — the preview-2 args path, both read_dir
// paths, the extern list wrappers — carried the same bare prefix.
//
// This is an emitted-code assertion rather than a runtime one on purpose: the
// symptom depends on what happens to precede the allocation, which is why it
// never reduced to a small program. The header either gets written or it does
// not, and that is decidable here.
func TestArrayReturningHelpersWriteRcHeader(t *testing.T) {
	// Every name resolves to 0 — the bodies are inspected for shape, never run.
	idxs := map[string]uint32{}
	local := func(n uint32) func([]byte) []byte {
		return func(b []byte) []byte { return inst.InstLocalGet(b, n) }
	}
	cases := []struct {
		name string
		body []byte
		// dataLocal holds the array's data pointer; countLocal its element
		// count, which is both the capacity and the length for these helpers.
		dataLocal, countLocal uint32
		rc                    int32
	}{
		// The cached argv array is a process-lifetime singleton handed to
		// every caller by pointer, so its header is the static sentinel: no
		// one scope-exit dec can be the last.
		{"__fern_args", buildArgsBody(idxs), 4, 0, arrRcStatic},
		{"__fern_args (preview 2)", buildArgsBodyP2(idxs), 2, 1, arrRcStatic},
		{"__fern_read_dir", buildReadDirBody(idxs), 13, 11, arrRcOwned},
		{"__fern_read_dir (preview 2)", buildReadDirBodyP2(idxs), 10, 7, arrRcOwned},
		{"__alloc_u8", buildAllocU8Body(idxs), 1, 0, arrRcOwned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			count := local(c.countLocal)
			want := emitArrHeaderStore(nil, c.dataLocal, c.rc, count, count)
			if !bytes.Contains(c.body, want) {
				t.Errorf("%s does not write the cap / rc / len header for its result array", c.name)
			}
			// A header written with rc = 1 where the sentinel belongs frees a
			// shared array on its first drop, so pin the rc word too.
			other := arrRcOwned
			if c.rc == arrRcOwned {
				other = arrRcStatic
			}
			if bytes.Contains(c.body, emitArrHeaderStore(nil, c.dataLocal, other, count, count)) {
				t.Errorf("%s writes the wrong refcount into its array header", c.name)
			}
		})
	}
}
