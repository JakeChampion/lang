package ambient

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// enforce loads, folds and checks src the way `fern -check` does, then
// runs the rule over it.
func enforce(t *testing.T, src string) []Violation {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	return Enforce(prog)
}

const handlerTail = `
    return http.ok("");
}
function main(): i32 { return 0; }
`

func TestReportsEffectsAroundTheBag(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string // "builtin@capability" per violation, in order
	}{
		{
			name: "every ambient builtin in the body, one violation per capability",
			src: `import "std/http";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    eprint("hit");
    var a: i64 = now_unix_ms();
    var b: i64 = monotonic_ns();
    var c: i32 = random_i32();
    match (env("HOME")) { Some(v) => { }, None => { } }` + handlerTail,
			want: []string{"env@env", "eprint@log", "now_unix_ms@now", "random_i32@random"},
		},
		{
			name: "an effect one call deeper is the handler's",
			src: `import "std/http";
function helper(): void { eprint("deep"); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    helper();` + handlerTail,
			want: []string{"eprint@log"},
		},
		{
			name: "through the bag is not a finding",
			src: `import "std/http";
import "std/platform";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    plat.log("hit");
    var a: i64 = plat.now_ms();
    match (plat.env("HOME")) { Some(v) => { }, None => { } }` + handlerTail,
			want: nil,
		},
		{
			name: "the bag is found by type, in any position and under any name",
			src: `import "std/http";
function route(bag: Platform, req: HttpRequest): HttpResponse {
    eprint("hit");` + handlerTail,
			want: []string{"eprint@log"},
		},
		{
			name: "a function without a bag is left alone",
			src: `import "std/http";
function helper(): i32 {
    eprint("hit");
    return 0;
}
function main(): i32 { return helper(); }
`,
			want: nil,
		},
		{
			name: "a builtin with no bag equivalent is still an effect",
			src: `import "std/http";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var xs: string[] = args();` + handlerTail,
			want: []string{"args@args"},
		},
		{
			name: "a lambda in the body is walked",
			src: `import "std/http";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var f: () => void = (): void => { eprint("hit"); };
    f();` + handlerTail,
			want: []string{"eprint@log"},
		},
		{
			name: "a function value named in the body is followed",
			src: `import "std/http";
function noisy(): void { eprint("hit"); }
function run(f: () => void): void { f(); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    run(noisy);` + handlerTail,
			want: []string{"eprint@log"},
		},
		{
			name: "a local spelled like an effectful function is not that function",
			src: `import "std/http";
function noisy(): i32 { eprint("hit"); return 1; }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var noisy: i32 = 2;
    var y: i32 = noisy + 1;` + handlerTail,
			want: nil,
		},
		{
			name: "a local closure spelled like an effectful function is not that function",
			src: `import "std/http";
function noisy(): void { eprint("hit"); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var noisy: () => void = (): void => {};
    noisy();` + handlerTail,
			want: nil,
		},
		{
			name: "a value whose target the walk cannot name charges nothing",
			src: `import "std/http";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var f: (i32) => i32 = (x: i32): i32 => x + 1;
    var y: i32 = f(1);` + handlerTail,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, v := range enforce(t, tc.src) {
				got = append(got, v.Builtin+"@"+v.Capability)
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The chain is the message's argument: it names the path from the handler
// to the builtin, so the reader knows which helper to hand the bag to.
func TestMessageNamesTheChainAndTheBag(t *testing.T) {
	vs := enforce(t, `import "std/http";
function deep(): void { eprint("x"); }
function mid(): void { deep(); }
function handle(req: HttpRequest, bag: Platform): HttpResponse {
    mid();`+handlerTail)
	if len(vs) != 1 {
		t.Fatalf("got %d violations, want 1", len(vs))
	}
	msg := vs[0].Message("/__fern_source__/main.fern")
	for _, want := range []string{"handler `handle`", "reaches `eprint` (`log`)", "bag `bag`", "handle -> mid -> deep -> eprint", "call `bag.log(…)`"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "declared in module") {
		t.Errorf("an entry-module handler must not be reported as declared elsewhere:\n%s", msg)
	}
	vs = enforce(t, `import "std/http";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var xs: string[] = args();`+handlerTail)
	if msg := vs[0].Message("/__fern_source__/main.fern"); !strings.Contains(msg, "nothing on the bag stands in for it") {
		t.Errorf("a builtin without a bag equivalent should say so:\n%s", msg)
	}
}

// The suggestions name std/platform methods that exist, with the same
// arity as the builtin they replace, so a reader can apply one verbatim.
func TestSuggestionsExistInStdPlatform(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "stdlib", "std", "platform.fern"))
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]bool{}
	for _, m := range regexp.MustCompile(`pub function \(plat: Platform\) ([a-z_0-9]+)\(`).FindAllStringSubmatch(string(src), -1) {
		methods[m[1]] = true
	}
	for builtin, method := range BagMethods() {
		if !methods[method] {
			t.Errorf("suggestion for `%s` names `plat.%s`, which std/platform does not define", builtin, method)
		}
	}
}

// The bag's own methods are the sanctioned route: they reach the builtins
// by design and are not handlers, even though their receiver is a
// Platform.
func TestBagMethodsAreNotHandlers(t *testing.T) {
	vs := enforce(t, `import "std/http";
import "std/platform";
import "std/fetch";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var n: i32 = plat.fetch("127.0.0.1", 1, "/");`+handlerTail)
	if len(vs) != 0 {
		t.Errorf("std/fetch's bag method reported: %+v", vs)
	}
}
