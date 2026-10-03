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
	const decls = `struct serve__Config { backlog: i32 }
function serve__config(): serve__Config { return serve__Config { backlog: 128 }; }
function serve____init_platform(): Platform { return Platform { version: 3, mode: 0, sink: cell_new(""), handle: 0 }; }
function serve__supervise(port: i32, opts: serve__Config, handler: (HttpRequest, Platform) => HttpResponse): i32 { return 0; }
function serve__supervise_shutdown(port: i32, opts: serve__Config, handler: (HttpRequest, Platform) => HttpResponse, shutdown: (string) => void): i32 { return 0; }
function serve__supervise_with[S](port: i32, opts: serve__Config, init: S, handler: (S, HttpRequest, Platform) => (S, HttpResponse)): i32 { return 0; }
function serve__supervise_with_shutdown[S](port: i32, opts: serve__Config, init: S, handler: (S, HttpRequest, Platform) => (S, HttpResponse), shutdown: (string, S) => void): i32 { return 0; }
function serve__run(port: i32, opts: serve__Config, handler: (HttpRequest, Platform) => HttpResponse): i32 { return 0; }
function serve__run_shutdown(port: i32, opts: serve__Config, handler: (HttpRequest, Platform) => HttpResponse, shutdown: (string) => void): i32 { return 0; }
function serve__run_with[S](port: i32, opts: serve__Config, init: S, handler: (S, HttpRequest, Platform) => (S, HttpResponse)): i32 { return 0; }
function serve__run_with_shutdown[S](port: i32, opts: serve__Config, init: S, handler: (S, HttpRequest, Platform) => (S, HttpResponse), shutdown: (string, S) => void): i32 { return 0; }
function serve____port_from_env(name: string, def: i32): i32 { return def; }
`
	const response = `HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] } }`
	const stateless = `function handle(req: HttpRequest, plat: Platform): HttpResponse { return ` + response + `; }`
	const stateful = `function handle(state: i32, req: HttpRequest, plat: Platform): (i32, HttpResponse) { return (state, ` + response + `); }`
	const missingState = "handler takes a state parameter, but no `init` produces the state to thread through it"
	const droppedState = "`init` returns a value, but the handler takes no state parameter to thread it through"
	const hookNeedsInit = "`shutdown` takes a state parameter, but no `init` produces the state to hand it"
	const hookDropsState = "`shutdown` takes no state parameter, but the handler threads a state it would drop"
	const initParams = "`init` takes 2 parameters; it takes none, or the platform alone"
	const initParam = "`init` takes 1 parameters; it takes none, or the platform alone"
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
		{"shutdown without state", stateless + "\nfunction shutdown(reason: string): void { print(reason); }", ""},
		{"shutdown with state", "function init(): i32 { return 7; }\n" + stateful + "\nfunction shutdown(reason: string, n: i32): void { print(reason); }", ""},
		{"stateful shutdown without init", stateless + "\nfunction shutdown(reason: string, n: i32): void { print(reason); }", hookNeedsInit},
		{"stateless shutdown drops the state", "function init(): i32 { return 7; }\n" + stateful + "\nfunction shutdown(reason: string): void { print(reason); }", hookDropsState},
		{"init takes the platform", "function init(plat: Platform): i32 { return 7; }\n" + stateful, ""},
		{"init answers the config", "function init(): serve__Config { return serve__config(); }\n" + stateless, ""},
		{"init answers the config beside the state", "function init(plat: Platform): (serve__Config, i32) { return (serve__config(), 7); }\n" + stateful, ""},
		{"config is not a state", "function init(): serve__Config { return serve__config(); }\n" + stateful, missingState},
		{"a Config of the program's own is a state", "struct Config { n: i32 }\nfunction init(): Config { return Config { n: 1 }; }\nfunction handle(c: Config, req: HttpRequest, plat: Platform): (Config, HttpResponse) { return (c, " + response + "); }", ""},
		{"state beside the config nobody takes", "function init(): (serve__Config, i32) { return (serve__config(), 7); }\n" + stateless, droppedState},
		{"init takes two parameters", "function init(plat: Platform, n: i32): i32 { return n; }\n" + stateful, initParams},
		{"init takes something else", "function init(n: i32): i32 { return n; }\n" + stateful, initParam},
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
				if tc.message == hookNeedsInit || tc.message == hookDropsState {
					decl = "\nfunction shutdown("
				}
				if tc.message == initParams || tc.message == initParam {
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
