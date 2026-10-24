package componenttype

import "testing"

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
		"wasi:io/error@0.2.0", "wasi:io/poll@0.2.0", "wasi:io/streams@0.2.0",
		"wasi:cli/stdin@0.2.0", "wasi:cli/stdout@0.2.0", "wasi:cli/stderr@0.2.0",
		"wasi:cli/environment@0.2.0", "wasi:cli/exit@0.2.0", "wasi:clocks/monotonic-clock@0.2.0",
		"wasi:clocks/wall-clock@0.2.0", "wasi:filesystem/types@0.2.0",
		"wasi:filesystem/preopens@0.2.0", "wasi:sockets/network@0.2.0",
		"wasi:sockets/instance-network@0.2.0", "wasi:sockets/tcp@0.2.0",
		"wasi:sockets/tcp-create-socket@0.2.0", "wasi:sockets/udp@0.2.0",
		"wasi:sockets/udp-create-socket@0.2.0", "wasi:random/random@0.2.0",
	}
	if len(order) != len(wantOrder) {
		t.Fatalf("interface count = %d, want %d (%v)", len(order), len(wantOrder), order)
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Errorf("interface %d = %q, want %q", i, order[i], wantOrder[i])
		}
	}

	checkInv(t, byName, "wasi:io/error@0.2.0", nil, []string{"error"})
	checkInv(t, byName, "wasi:io/streams@0.2.0",
		[]string{
			"[method]input-stream.read", "[method]input-stream.blocking-read",
			"[method]input-stream.subscribe",
			"[method]output-stream.check-write", "[method]output-stream.write",
			"[method]output-stream.blocking-write-and-flush", "[method]output-stream.blocking-flush",
			"[method]output-stream.subscribe",
		},
		[]string{"input-stream", "output-stream"})
	checkInv(t, byName, "wasi:cli/stdout@0.2.0", []string{"get-stdout"}, nil)
	checkInv(t, byName, "wasi:io/poll@0.2.0", []string{"[method]pollable.block", "poll"}, []string{"pollable"})
	checkInv(t, byName, "wasi:filesystem/preopens@0.2.0", []string{"get-directories"}, nil)
	checkInv(t, byName, "wasi:random/random@0.2.0", []string{"get-random-bytes", "get-random-u64"}, nil)
	checkInv(t, byName, "wasi:cli/environment@0.2.0", []string{"get-arguments", "get-environment"}, nil)
	checkInv(t, byName, "wasi:cli/exit@0.2.0", []string{"exit"}, nil)
	checkInv(t, byName, "wasi:clocks/monotonic-clock@0.2.0", []string{"now", "subscribe-duration"}, nil)
	checkInv(t, byName, "wasi:sockets/udp-create-socket@0.2.0", []string{"create-udp-socket"}, nil)
	checkInv(t, byName, "wasi:sockets/udp@0.2.0",
		[]string{
			"[method]incoming-datagram-stream.receive",
			"[method]incoming-datagram-stream.subscribe",
			"[method]outgoing-datagram-stream.check-send",
			"[method]outgoing-datagram-stream.send",
			"[method]outgoing-datagram-stream.subscribe",
			"[method]udp-socket.start-bind",
			"[method]udp-socket.finish-bind",
			"[method]udp-socket.stream",
			"[method]udp-socket.local-address",
			"[method]udp-socket.remote-address",
		},
		[]string{"incoming-datagram-stream", "outgoing-datagram-stream", "udp-socket"})
}

func TestClassifyEqAliasedRecordResult(t *testing.T) {
	w, err := DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	var wall *WorldInterface
	for _, wi := range w.Interfaces() {
		if wi.Name == "wasi:clocks/wall-clock@0.2.0" {
			c := wi
			wall = &c
			break
		}
	}
	if wall == nil {
		t.Fatal("wall-clock interface not found in fern world")
	}
	var now *WorldFunc
	for i := range wall.FuncSigs {
		if wall.FuncSigs[i].Name == "now" {
			now = &wall.FuncSigs[i]
			break
		}
	}
	if now == nil {
		t.Fatal("wall-clock `now` not found")
	}
	if got := wall.Classify(*now); got != KindMem {
		t.Errorf("Classify(wall-clock.now) = %v, want KindMem (datetime is a 2-field record returned via memory)", got)
	}
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

	br := findSig(t, streams, "[method]input-stream.blocking-read")
	rd := streams.ResolveDef(br.Sig.Result)
	if rd == nil || rd.Tag != tagResult || rd.has_ok() == false {
		t.Fatalf("blocking-read result = %v", rd)
	}
	if okd := streams.ResolveDef(rd.Ok); okd == nil || okd.Tag != tagList {
		t.Errorf("blocking-read result ok = %v, want list", okd)
	}

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

func TestWorldExportedInterfaces(t *testing.T) {
	w, err := DecodeWorld("http")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	exps := w.ExportedInterfaces()
	if len(exps) == 0 {
		t.Fatal("http: no exported interfaces lifted")
	}
	var handler *WorldInterface
	for i := range exps {
		if exps[i].Name == "wasi:http/incoming-handler@0.2.0" {
			handler = &exps[i]
		}
	}
	if handler == nil {
		t.Fatalf("http: missing exported interface wasi:http/incoming-handler@0.2.0; got %v", exps)
	}
	found := false
	for _, f := range handler.Funcs {
		if f == "handle" {
			found = true
		}
	}
	if !found {
		t.Errorf("incoming-handler exports = %v, want it to include handle", handler.Funcs)
	}
	if _, ok := w.ExportFunc("wasi:http/incoming-handler@0.2.0", "handle"); !ok {
		t.Error("ExportFunc(incoming-handler, handle) not found")
	}
	if _, ok := w.ExportFunc("wasi:http/incoming-handler@0.2.0", "nope"); ok {
		t.Error("ExportFunc should not find a nonexistent export")
	}
}
