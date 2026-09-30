package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostAmbientEffectDifferentialX86_64 is the parity gate for the
// ambient-effect rule (E080): a handler reaching a host effect around its
// `Platform` bag is refused by both compilers at the same declaration, with
// the same handler, builtin and capability named, and a handler reaching it
// through the bag is accepted by both.
//
// The chain is not compared: native's walk orders callees by first use in
// a body and the self-host's by declaration, so two paths to one builtin
// may print in a different order. The verdict, the site and the effect are
// what a program builds or fails on.
func TestSelfHostAmbientEffectDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ambient differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const tail = `
    return http.ok("");
}
function main(): i32 { return 0; }
`
	cases := []struct {
		name string
		src  string
		want []string // "line:col handler builtin capability" per E080
	}{
		{
			name: "direct",
			src: `import "std/http";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    eprint("hit");` + tail,
			want: []string{"2:1 handle eprint log"},
		},
		{
			name: "through a helper",
			src: `import "std/http";
function helper(): void { eprint("hit"); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    helper();` + tail,
			want: []string{"3:1 handle eprint log"},
		},
		{
			name: "one per capability, in vocabulary order",
			src: `import "std/http";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var a: i64 = now_unix_ms();
    match (env("HOME")) { Some(v) => { }, None => { } }` + tail,
			want: []string{"2:1 handle env env", "2:1 handle now_unix_ms now"},
		},
		{
			name: "the bag by type, in any position",
			src: `import "std/http";
function route(bag: Platform, req: HttpRequest): HttpResponse {
    var c: i32 = random_i32();` + tail,
			want: []string{"2:1 route random_i32 random"},
		},
		{
			name: "through the bag",
			src: `import "std/http";
import "std/platform";
function helper(plat: Platform): void { plat.log("hit"); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    helper(plat);
    var a: i64 = plat.now_ms();` + tail,
			want: nil,
		},
		{
			name: "a method reached by a method call",
			src: `import "std/http";
struct Sock { fd: i32 }
function (s: Sock) close(): i32 { eprint("closing"); return s.fd; }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var s: Sock = Sock { fd: 3 };
    var n: i32 = s.close();` + tail,
			want: []string{"4:1 handle eprint log"},
		},
		{
			// #10619: std/url's url_parse has a `var close`, which reached
			// std/async's reactor `close` and refused examples/wasm/url_router.
			name: "a local named like a method",
			src: `import "std/http";
struct Sock { fd: i32 }
function (s: Sock) close(): i32 { eprint("closing"); return s.fd; }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var close: i32 = 3;
    var n: i32 = close + 1;` + tail,
			want: nil,
		},
		{
			name: "a local named like a function",
			src: `import "std/http";
function noisy(): i32 { eprint("hit"); return 1; }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var noisy: i32 = 2;
    var n: i32 = noisy + 1;` + tail,
			want: nil,
		},
		{
			// std/unicode's case mapping has a `var mid`, which std/http's own
			// handlers reach through HeaderMap.set.
			name: "a std local named like an entry function",
			src: `import "std/http";
function mid(): void { eprint("hit"); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var h: HttpResponse = http.ok("");
    h.headers.set("X-Mode", "Plain");` + tail + `function run(): void { mid(); }
`,
			want: nil,
		},
		{
			name: "a function without a bag",
			src: `function helper(): i32 { eprint("hit"); return 0; }
function main(): i32 { return helper(); }
`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			nativeOut, nativeErr := exec.Command(nativeBin, "-check", src).CombinedOutput()
			shOut, shErr := exec.Command(driverBin, "-check", src, stdlib).CombinedOutput()
			want := strings.Join(tc.want, "; ")
			if got := strings.Join(e080Sites(string(nativeOut)), "; "); got != want {
				t.Errorf("native E080 = [%s], want [%s]\n%s", got, want, nativeOut)
			}
			if got := strings.Join(e080Sites(string(shOut)), "; "); got != want {
				t.Errorf("self-host E080 = [%s], want [%s]\n%s", got, want, shOut)
			}
			if (nativeErr == nil) != (shErr == nil) {
				t.Errorf("verdicts differ: native err=%v, self-host err=%v\nnative:\n%s\nself-host:\n%s", nativeErr, shErr, nativeOut, shOut)
			}
			if tc.want == nil && (nativeErr != nil || shErr != nil) {
				t.Errorf("a clean program was refused:\nnative:\n%s\nself-host:\n%s", nativeOut, shOut)
			}
		})
	}
}

// e080Sites reduces a compiler's output to its E080 diagnostics as
// `line:col handler builtin capability`, in emission order.
func e080Sites(out string) []string {
	var sites []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "error[E080]") {
			continue
		}
		pos := "<no position>"
		if i := strings.Index(line, ": error[E080]"); i >= 0 {
			head := line[:i]
			parts := strings.Split(head, ":")
			if len(parts) >= 2 {
				pos = parts[len(parts)-2] + ":" + parts[len(parts)-1]
			}
		}
		handler := between(line, "handler `", "`")
		builtin := between(line, "reaches `", "`")
		capability := between(line, "` (`", "`)")
		sites = append(sites, pos+" "+handler+" "+builtin+" "+capability)
	}
	return sites
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
