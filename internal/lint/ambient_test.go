package lint_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/lint"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/stdlib"
)

// ambientFindings lints src at default config and keeps only the
// ambient-capability findings, so a fixture that also happens to be
// branch-heavy doesn't fold the complexity rule into the count.
func ambientFindings(t *testing.T, src string) []lint.Finding {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, src)
	}
	all, err := lint.File(lint.NewConfig(), "t.fern", src, prog)
	if err != nil {
		t.Fatal(err)
	}
	var out []lint.Finding
	for _, f := range all {
		if f.Rule == "ambient-capability" {
			out = append(out, f)
		}
	}
	return out
}

func TestAmbientCapabilityReportsHandlerEffects(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "every builtin on the bag is reported",
			src: `function handle(req: HttpRequest, plat: Platform): HttpResponse {
    eprint("hit");
    var a: i64 = now_unix_ms();
    var b: i64 = monotonic_ns();
    var c: i32 = random_i32();
    match (env("HOME")) { Some(v) => { }, None => { } }
    return HttpResponse { status: 200, body: "" };
}`,
			want: 5,
		},
		{
			// The bag is a handler's; a plain function has none to route
			// through, so the same call is not a finding there.
			name: "a non-handler is left alone",
			src: `function helper(): i32 {
    eprint("hit");
    return 0;
}`,
			want: 0,
		},
		{
			// A `handle` whose second parameter is not a Platform is not
			// the handler shape — nothing to suggest.
			name: "handle without a bag parameter",
			src: `function handle(a: i32, b: i32): i32 {
    eprint("hit");
    return 0;
}`,
			want: 0,
		},
		{
			name: "effects nested in control flow are found",
			src: `function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/") {
        while (false) { eprint("hit"); }
    }
    return HttpResponse { status: 200, body: "" };
}`,
			want: 1,
		},
		{
			// Reaching a capability the bag does not carry is out of
			// scope: the target gate (E066) is what answers for those.
			name: "a builtin with no bag equivalent is not reported",
			src: `function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var xs: string[] = args();
    return HttpResponse { status: 200, body: "" };
}`,
			want: 0,
		},
		{
			// A local bound to the same name is not the builtin, and the
			// parse tree carries no resolution to ask — so the rule holds
			// its tongue rather than pointing at the wrong call.
			name: "a local shadowing the builtin is not reported",
			src: `function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var eprint: (string) => void = plat.log;
    eprint("hit");
    return HttpResponse { status: 200, body: "" };
}`,
			want: 0,
		},
		{
			name: "suppressed at the site",
			src: `function handle(req: HttpRequest, plat: Platform): HttpResponse {
    // fern-lint: allow ambient-capability
    eprint("hit");
    return HttpResponse { status: 200, body: "" };
}`,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(ambientFindings(t, tc.src)); got != tc.want {
				t.Errorf("got %d findings, want %d", got, tc.want)
			}
		})
	}
}

// The help text names the handler's own parameter, because that is what the
// reader has to type — a fixed `plat.` would be wrong for any handler that
// spells its bag differently.
func TestAmbientCapabilityNamesTheBagParameter(t *testing.T) {
	fs := ambientFindings(t, `function handle(req: HttpRequest, host: Platform): HttpResponse {
    eprint("hit");
    return HttpResponse { status: 200, body: "" };
}`)
	if len(fs) != 1 {
		t.Fatalf("got %d findings, want 1", len(fs))
	}
	if !strings.Contains(fs[0].Help, "`host.log(…)`") {
		t.Errorf("help should suggest the handler's own bag name, got: %s", fs[0].Help)
	}
	if !strings.Contains(fs[0].Msg, "`host`") {
		t.Errorf("message should name the handler's own bag, got: %s", fs[0].Msg)
	}
}

// A zero-argument builtin gets a zero-argument suggestion: `plat.now_ms()`,
// not `plat.now_ms(…)`.
func TestAmbientCapabilitySuggestionArity(t *testing.T) {
	fs := ambientFindings(t, `function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var a: i64 = now_unix_ms();
    return HttpResponse { status: 200, body: "" };
}`)
	if len(fs) != 1 {
		t.Fatalf("got %d findings, want 1", len(fs))
	}
	if !strings.Contains(fs[0].Help, "`plat.now_ms()`") {
		t.Errorf("help should suggest an empty argument list, got: %s", fs[0].Help)
	}
}

// The rule suggests methods; std/platform is where they live. A rename on
// either side without the other leaves the linter pointing at a method that
// does not exist, which no other test would catch.
func TestAmbientCapabilitySuggestionsExistInStdPlatform(t *testing.T) {
	src, ok := stdlib.Resolve("std/platform")
	if !ok {
		t.Fatal("std/platform is not in the embedded stdlib")
	}
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse std/platform: %v", err)
	}
	have := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.Receiver == nil {
			continue
		}
		if st, ok := fn.Receiver.Type.(ast.StructType); ok && st.Name == "Platform" {
			have[fn.Name] = true
		}
	}
	for builtin, method := range lint.BagMethods() {
		if !have[method] {
			t.Errorf("rule suggests `plat.%s()` for %s, but std/platform declares no such Platform method", method, builtin)
		}
	}
}
