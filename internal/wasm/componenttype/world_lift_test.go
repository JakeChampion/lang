package componenttype

import "testing"

// TestWorldInterfaces checks the lifted per-interface model against the fern
// world: import order, and each interface's exported functions + resources
// (cross-checked with `wasm-tools component wit` on fern.bin).
func TestWorldInterfaces(t *testing.T) {
	w, err := DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	ifaces := w.Interfaces()

	byName := map[string]WorldInterface{}
	var order []string
	for _, wi := range ifaces {
		byName[wi.Name] = wi
		order = append(order, wi.Name)
	}

	wantOrder := []string{
		"wasi:io/error@0.2.0", "wasi:io/streams@0.2.0", "wasi:cli/stdin@0.2.0",
		"wasi:cli/stdout@0.2.0", "wasi:cli/stderr@0.2.0", "wasi:io/poll@0.2.0",
		"wasi:clocks/wall-clock@0.2.0", "wasi:filesystem/types@0.2.0",
		"wasi:filesystem/preopens@0.2.0", "wasi:sockets/network@0.2.0",
		"wasi:sockets/instance-network@0.2.0", "wasi:sockets/tcp@0.2.0",
		"wasi:sockets/tcp-create-socket@0.2.0", "wasi:random/random@0.2.0",
	}
	if len(order) != len(wantOrder) {
		t.Fatalf("interface count = %d, want %d (%v)", len(order), len(wantOrder), order)
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Errorf("interface %d = %q, want %q", i, order[i], wantOrder[i])
		}
	}

	// Spot-check inventories against the known WIT.
	checkInv(t, byName, "wasi:io/error@0.2.0", nil, []string{"error"})
	checkInv(t, byName, "wasi:io/streams@0.2.0",
		[]string{
			"[method]input-stream.read", "[method]input-stream.blocking-read",
			"[method]output-stream.check-write", "[method]output-stream.write",
			"[method]output-stream.blocking-write-and-flush", "[method]output-stream.blocking-flush",
		},
		[]string{"input-stream", "output-stream"})
	checkInv(t, byName, "wasi:cli/stdout@0.2.0", []string{"get-stdout"}, nil)
	checkInv(t, byName, "wasi:io/poll@0.2.0", []string{"[method]pollable.block"}, []string{"pollable"})
	checkInv(t, byName, "wasi:filesystem/preopens@0.2.0", []string{"get-directories"}, nil)
	checkInv(t, byName, "wasi:random/random@0.2.0", []string{"get-random-bytes", "get-random-u64"}, nil)
}

func checkInv(t *testing.T, by map[string]WorldInterface, name string, funcs, resources []string) {
	t.Helper()
	wi, ok := by[name]
	if !ok {
		t.Errorf("%s: not found", name)
		return
	}
	if !eqStrings(wi.Funcs, funcs) {
		t.Errorf("%s funcs = %v, want %v", name, wi.Funcs, funcs)
	}
	if !eqStrings(wi.Resources, resources) {
		t.Errorf("%s resources = %v, want %v", name, wi.Resources, resources)
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWorldInterfacesHTTP sanity-checks the http world lifts without error
// and surfaces its key interfaces.
func TestWorldInterfacesHTTP(t *testing.T) {
	w, err := DecodeWorld("http")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	ifaces := w.Interfaces()
	if len(ifaces) == 0 {
		t.Fatal("http: no interfaces lifted")
	}
	seen := map[string]bool{}
	for _, wi := range ifaces {
		seen[wi.Name] = true
	}
	for _, want := range []string{"wasi:http/types@0.2.0", "wasi:io/streams@0.2.0"} {
		if !seen[want] {
			t.Errorf("http: missing interface %q", want)
		}
	}
}

// TestWorldFuncSignatures checks P2 slice 2: exported functions resolve to
// their decoded signatures, and param/result types resolve to the right
// defined-type kind through the interface's local type space.
func TestWorldFuncSignatures(t *testing.T) {
	w, err := DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	by := map[string]WorldInterface{}
	for _, wi := range w.Interfaces() {
		by[wi.Name] = wi
	}

	streams := by["wasi:io/streams@0.2.0"]
	bwf := findSig(t, streams, "[method]output-stream.blocking-write-and-flush")
	if bwf.Sig == nil || len(bwf.Sig.Params) != 2 {
		t.Fatalf("bwf signature wrong: %+v", bwf.Sig)
	}
	// param 0 is the receiver handle (borrow), param 1 is contents: list<u8>.
	if d := streams.ResolveDef(bwf.Sig.Params[0].Ty); d == nil || d.Tag != tagBorrow {
		t.Errorf("bwf param0 = %v, want borrow", d)
	}
	if d := streams.ResolveDef(bwf.Sig.Params[1].Ty); d == nil || d.Tag != tagList {
		t.Errorf("bwf param1 = %v, want list", d)
	}
	if bwf.Sig.NamedResults {
		t.Errorf("bwf should have an unnamed result")
	}
	if d := streams.ResolveDef(bwf.Sig.Result); d == nil || d.Tag != tagResult {
		t.Errorf("bwf result = %v, want result", d)
	}

	// blocking-read returns result<list<u8>, stream-error>: the result's ok
	// payload is a list (drives mem+realloc classification later).
	br := findSig(t, streams, "[method]input-stream.blocking-read")
	rd := streams.ResolveDef(br.Sig.Result)
	if rd == nil || rd.Tag != tagResult || rd.has_ok() == false {
		t.Fatalf("blocking-read result = %v", rd)
	}
	if okd := streams.ResolveDef(rd.Ok); okd == nil || okd.Tag != tagList {
		t.Errorf("blocking-read result ok = %v, want list", okd)
	}

	// get-stdout: no params, returns own<output-stream> (a handle).
	gs := findSig(t, by["wasi:cli/stdout@0.2.0"], "get-stdout")
	if gs.Sig == nil || len(gs.Sig.Params) != 0 {
		t.Fatalf("get-stdout params = %v", gs.Sig)
	}
	if d := by["wasi:cli/stdout@0.2.0"].ResolveDef(gs.Sig.Result); d == nil || d.Tag != tagOwn {
		t.Errorf("get-stdout result = %v, want own", d)
	}
}

func (d DefinedType) has_ok() bool { return d.HasOk }

func findSig(t *testing.T, wi WorldInterface, name string) WorldFunc {
	t.Helper()
	for _, f := range wi.FuncSigs {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("%s: no func %q", wi.Name, name)
	return WorldFunc{}
}
