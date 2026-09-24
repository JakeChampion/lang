package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// #8756, the self-host half of #8755. A struct carrying an Option field must
// drop deep when overwritten, and a fresh box handed straight on as the
// receiver of a struct-returning call (`id_w(o).put(s)`) must be released
// after the call, with the identity case still owing one count. These are
// native's three shapes (TestX86_64OptFieldStructChainLeakFree); the
// self-host once kept every superseded box, 2 GB over 20,000 rounds.
//
// The reclaim rests on the checker's annotations of the Option field, so the
// leg compiles through the self-host CLI.
var optFieldStructChainCases = []struct{ name, src string }{
	// The chained receiver: `o = id_w(o).put(..)`.
	{"chained-receiver", `struct W { buf: string, err: Option[i32] }
function (w: W) put(s: string): W { return W { ...w, buf: w.buf + s }; }
function id_w(w: W): W { return w; }
function main(): i32 {
    var o: W = W { buf: "", err: None };
    var i: i32 = 0;
    while (i < 2000) { o = id_w(o).put("abcdefghij"); i = i + 1; }
    if (o.buf.len() != 20000) { return 1; }
    return 0;
}`},
	// The borrowed field through a helper that appends more than once,
	// where put may hand its receiver back (as BufWriter.write_string
	// does on its error path): the local it initialises is tainted,
	// and its overwrite rests on the struct being self-drop-safe.
	{"borrowed-field-helper", `struct W { buf: string, err: Option[string] }
struct S { o: W, n: i32 }
function (w: W) put(s: string): W {
    if (s.len() == 0) { return w; }
    return W { ...w, buf: w.buf + s };
}
function rep(o: W): W {
    var out: W = o.put("abcde");
    out = out.put("fghij");
    return out;
}
function main(): i32 {
    var st: S = S { o: W { buf: "", err: None }, n: 0 };
    var i: i32 = 0;
    while (i < 2000) {
        var r: W = rep(st.o);
        st = S { ...st, o: r };
        i = i + 1;
    }
    if (st.o.buf.len() != 20000) { return 1; }
    return 0;
}`},
	// Recursive threading that returns its parameter at the base case,
	// chained: the identity case owes the return transfer's count.
	{"recursive-threading", `struct W { buf: string, err: Option[i32] }
function (w: W) put(s: string): W { return W { ...w, buf: w.buf + s }; }
function digits(w: W, k: i32): W {
    if (k == 0) { return w; }
    return digits(w.put("x"), k - 1);
}
function main(): i32 {
    var o: W = W { buf: "", err: None };
    var i: i32 = 0;
    while (i < 2000) { o = digits(o, 5).put(" "); i = i + 1; }
    if (o.buf.len() != 12000) { return 1; }
    return 0;
}`},
}

func TestSelfHostOptFieldStructChainX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optFieldStructChainCases {
		t.Run(tc.name, func(t *testing.T) {
			src := mustWrite(t, t.TempDir(), "main.fern", tc.src)
			stderr, code := runCaptureStderrExit(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"))
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			if allocs, frees, live := leakSummaryOf(t, tc.name, stderr); allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("allocs=%d frees=%d live_bytes=%d — a superseded box or its buffer is not released", allocs, frees, live)
			}
			stderr, code = runCaptureStderrExit(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"))
			if code != 0 {
				t.Fatalf("exit %d under the sanitizer, want 0\n%s", code, stderr)
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak") {
					t.Errorf("%s", line)
				}
			}
		})
	}
}

func TestSelfHostOptFieldStructChainWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm opt-field struct chain census")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range optFieldStructChainCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := cli.emit(t, mustWrite(t, t.TempDir(), "main.fern", tc.src), "wasm32-wasi", "FERN_LEAKCHECK=1")
			stderr, code := runWasmCensus(t, wat)
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			if allocs, frees, live := leakSummaryOf(t, tc.name, stderr); allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("allocs=%d frees=%d live_bytes=%d — a superseded box or its buffer is not released", allocs, frees, live)
			}
		})
	}
}
