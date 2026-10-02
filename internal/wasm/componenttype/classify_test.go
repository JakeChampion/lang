package componenttype

import "testing"

// TestClassify checks the canonical-ABI classifier reproduces the lowering
// kinds the composer hard-codes (internal/wasm/component/compose_unified.go),
// derived purely from the fern world's decoded signatures. It covers all
// three kinds and the subtle cases: a no-heap result that still needs memory
// because it returns indirectly (read-via-stream, start-bind), a scalar
// handle return that does not (subscribe, instance-network, get-stdout), a
// heap param (write), and heap results (blocking-read, get-directories,
// get-random-bytes).
func TestClassify(t *testing.T) {
	w, err := DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	by := map[string]WorldInterface{}
	for _, wi := range w.Interfaces() {
		by[wi.Name] = wi
	}

	cases := []struct {
		iface string
		fn    string
		want  LowerKind
	}{
		{"wasi:cli/stdout@0.2.0", "get-stdout", KindNoOpt},
		{"wasi:cli/stderr@0.2.0", "get-stderr", KindNoOpt},
		{"wasi:io/poll@0.2.0", "[method]pollable.block", KindNoOpt},
		{"wasi:sockets/instance-network@0.2.0", "instance-network", KindNoOpt},
		{"wasi:sockets/tcp@0.2.0", "[method]tcp-socket.subscribe", KindNoOpt},
		{"wasi:random/random@0.2.0", "get-random-u64", KindNoOpt},

		{"wasi:io/streams@0.2.0", "[method]output-stream.blocking-write-and-flush", KindMem},
		{"wasi:io/streams@0.2.0", "[method]output-stream.write", KindMem},
		{"wasi:filesystem/types@0.2.0", "[method]descriptor.open-at", KindMem},
		{"wasi:filesystem/types@0.2.0", "[method]descriptor.read-via-stream", KindMem},
		{"wasi:sockets/tcp@0.2.0", "[method]tcp-socket.start-bind", KindMem},
		{"wasi:sockets/tcp@0.2.0", "[method]tcp-socket.accept", KindMem},
		{"wasi:sockets/tcp-create-socket@0.2.0", "create-tcp-socket", KindMem},

		{"wasi:io/streams@0.2.0", "[method]input-stream.blocking-read", KindMemRealloc},
		{"wasi:io/streams@0.2.0", "[method]input-stream.read", KindMemRealloc},
		{"wasi:filesystem/preopens@0.2.0", "get-directories", KindMemRealloc},
		{"wasi:random/random@0.2.0", "get-random-bytes", KindMemRealloc},
	}

	for _, c := range cases {
		wi, ok := by[c.iface]
		if !ok {
			t.Errorf("%s: interface not found", c.iface)
			continue
		}
		f := lookupSig(wi, c.fn)
		if f.Sig == nil && c.fn != "" {
			t.Errorf("%s %s: no signature", c.iface, c.fn)
			continue
		}
		if got := wi.Classify(f); got != c.want {
			t.Errorf("%s %s: classify = %s, want %s", c.iface, c.fn, got, c.want)
		}
	}
}

func lookupSig(wi WorldInterface, name string) WorldFunc {
	for _, f := range wi.FuncSigs {
		if f.Name == name {
			return f
		}
	}
	return WorldFunc{}
}

// A type a `use` brings in from another interface classifies as what it is:
// `outgoing-handler.handle` of the proxy world answers `result<own, error-code>`
// with `error-code` declared in wasi:http/types, whose cases carry strings, so
// the lowering needs realloc exactly as `future-incoming-response.get`, which
// reads the same variant from inside the types interface, does. Before the
// owner resolution the `use`d slot read as an opaque scalar and the lowering
// dropped the realloc option the canonical ABI requires.
func TestClassifyUsedTypeFromAnotherInterface(t *testing.T) {
	w, err := DecodeWorld("proxy")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	want := map[[2]string]LowerKind{
		{"wasi:http/outgoing-handler@0.2.0", "handle"}:                           KindMemRealloc,
		{"wasi:http/types@0.2.0", "[method]future-incoming-response.get"}:        KindMemRealloc,
		{"wasi:http/types@0.2.0", "[method]outgoing-request.set-method"}:         KindMem,
		{"wasi:http/types@0.2.0", "[method]outgoing-request.set-authority"}:      KindMem,
		{"wasi:http/types@0.2.0", "[method]request-options.set-connect-timeout"}: KindNoOpt,
		{"wasi:http/types@0.2.0", "[method]incoming-response.status"}:            KindNoOpt,
	}
	seen := 0
	for _, wi := range w.Interfaces() {
		for _, f := range wi.FuncSigs {
			k, ok := want[[2]string{wi.Name, f.Name}]
			if !ok {
				continue
			}
			seen++
			if got := wi.Classify(f); got != k {
				t.Errorf("%s %s classifies as %s, want %s", wi.Name, f.Name, got, k)
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d of %d functions in the proxy world", seen, len(want))
	}
}
