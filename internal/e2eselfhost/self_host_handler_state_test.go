package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

// Exercise the production checker directly, so the semantic regression also
// runs on cross hosts without rebuilding a second-generation compiler driver.
func TestSelfHostHandlerStateX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "checker_codes_run.fern", "checker_codes_run")
	copySelfHostDriver(t, dir, "checker_run.fern")
	formattedBin := buildSelfHostBin(t, gcc, dir, "checker_run.fern", "checker_run")
	const decls = `function tcp_serve(port: i32, handler: (HttpRequest, Platform) => HttpResponse): i32 { return 0; }
function tcp_serve_with[S](port: i32, init: S, handler: (S, HttpRequest, Platform) => (S, HttpResponse)): i32 { return 0; }
function __port_from_env(name: string, def: i32): i32 { return def; }
`
	const response = `HttpResponse { status: 200, body: "ok", headers: HeaderMap { names: [], values: [] } }`
	const stateless = `function handle(req: HttpRequest, plat: Platform): HttpResponse { return ` + response + `; }`
	const stateful = `function handle(state: i32, req: HttpRequest, plat: Platform): (i32, HttpResponse) { return (state, ` + response + `); }`
	const missingState = "handler takes a state parameter, but no `init` produces the state to thread through it"
	const droppedState = "`init` returns a value, but the handler takes no state parameter to thread it through"
	cases := []struct{ name, src, message string }{
		{"missing init", stateful, missingState},
		{"void init", "function init(): void {}\n" + stateful, missingState},
		{"unspecified init", "function init() {}\n" + stateful, missingState},
		{"unused state", "function init(): i32 { return 7; }\n" + stateless, droppedState},
		{"reversed declarations", stateless + "\nfunction init(): i32 { return 7; }", droppedState},
		{"valid state", "function init(): i32 { return 7; }\n" + stateful, ""},
		{"valid void init", "function init(): void {}\n" + stateless, ""},
		{"valid no init", stateless, ""},
		{"explicit main owns unused state", "function init(): i32 { return 7; }\n" + stateless + "\nfunction main(): i32 { return init(); }", ""},
		{"explicit main owns missing state", stateful + "\nfunction main(): i32 { return 0; }", ""},
		{"no handle", "function init(): i32 { return 7; }", ""},
		{"method is not init", "struct S { n: i32 }\nfunction (s: S) init(): i32 { return s.n; }\n" + stateful, missingState},
		{"method is not handle", "struct S { n: i32 }\nfunction init(): i32 { return 7; }\nfunction (s: S) handle(): i32 { return s.n; }", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + tc.src
			prog, err := parser.Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = checker.Check(prog)
			if tc.message == "" {
				if err != nil {
					t.Fatalf("native rejected valid pairing: %v", err)
				}
			} else if err == nil || !strings.Contains(diag.Format("state.fern", src, err), "E075") || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("native: want E075 %q, got %v", tc.message, err)
			}
			stdout, stderr, code := runDriverAllowFail(t, runner, bin, src)
			output := string(stdout)
			if len(stderr) != 0 {
				t.Fatalf("checker driver stderr: %s", stderr)
			}
			if tc.message == "" {
				if code != 0 || strings.TrimSpace(output) != "" {
					t.Fatalf("self-host rejected valid pairing: exit %d\n%s", code, output)
				}
			} else if code == 0 || !strings.Contains(output, "E075\t"+tc.message) {
				t.Fatalf("self-host: want E075 %q, got exit %d\n%s", tc.message, code, output)
			}
			if tc.message != "" {
				decl := "\nfunction handle("
				if tc.message == droppedState {
					decl = "\nfunction init("
				}
				at := strings.Index(src, decl)
				if at < 0 {
					t.Fatal("fixture lacks the expected diagnostic declaration")
				}
				line := strings.Count(src[:at+1], "\n") + 1
				want := fmt.Sprintf("error[E075]: %s (%d:1)", tc.message, line)
				code, formatted := runSelfHostChecker(t, formattedBin, runner, src)
				if code != 1 || !strings.Contains(formatted, want) {
					t.Fatalf("want positioned diagnostic %q, got exit %d\n%s", want, code, formatted)
				}
			}
		})
	}
}
