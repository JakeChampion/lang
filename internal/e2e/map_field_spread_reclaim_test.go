package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A spread rebuild that replaces a struct's Map field releases the map it
// replaces (#10228). The reused box's displaced field went through a flat
// rc dec, which never frees, so every rebuild stranded a whole table. The
// `shared` case keeps a second holder of the old box, so the map must survive
// for it.
var mapFieldSpreadCases = []struct {
	name string
	body string
	want int
}{
	{"replace", `var b: Box = Box { m: m, tag: 1 };
    var m2: Map[i32, i32] = map_new(4);
    b = Box { ...b, m: m2 };
    return b.m.len();`, 0},
	{"loop", `var b: Box = Box { m: m, tag: 1 };
    var k: i32 = 0;
    while (k < 4) { b = Box { ...b, m: b.m.insert(10 + k, k) }; k = k + 1; }
    return b.m.len();`, 5},
	{"shared", `var b: Box = Box { m: m, tag: 1 };
    var c: Box = b;
    var m2: Map[i32, i32] = map_new(4);
    b = Box { ...b, m: m2 };
    return b.m.len() * 10 + c.m.len();`, 1},
}

func mapFieldSpreadSrc(body string) string {
	return `import "core/map";
struct Box { m: Map[i32, i32], tag: i32 }
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    ` + body + `
}`
}

func TestMapFieldSpreadRebuildReleasesTheOldMap(t *testing.T) {
	for _, tc := range mapFieldSpreadCases {
		src := mapFieldSpreadSrc(tc.body)
		t.Run(tc.name+"/x86-64", func(t *testing.T) {
			_, stderr, exit := runLeakCheckX86_64(t, src)
			checkMapReceiverCensus(t, tc.want, stderr, exit)
		})
		t.Run(tc.name+"/arm64", func(t *testing.T) {
			_, stderr, exit := runLeakCheckArm64(t, src)
			checkMapReceiverCensus(t, tc.want, stderr, exit)
		})
		t.Run(tc.name+"/wasm", func(t *testing.T) {
			stdout, stderr, exit := runLeakCheckWasm(t, src, false)
			if exit != 0 || strings.TrimSpace(stdout) != strconv.Itoa(tc.want) {
				t.Fatalf("exit %d stdout %q, want exit 0 printing %d; stderr: %s", exit, stdout, tc.want, stderr)
			}
			a, f, live := parseWasmLeakCheckLine(t, stderr)
			if a != f || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", a, f, live)
			}
		})
	}
}
