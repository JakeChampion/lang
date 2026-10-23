package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestHandlerNeedsServeImport(t *testing.T) {
	const handler = `function handle(req: HttpRequest, plat: platform__Host): HttpResponse {
    return HttpResponse { status: 200, body: BodyText("hello"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None };
}`
	for _, target := range []string{"", "x86-64-linux", "arm64-darwin", "wasm32-wasi", "wasm32-wasi-http"} {
		t.Run(target, func(t *testing.T) {
			source := hostStubDecls + handler
			prog, err := parser.Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = CheckTarget(prog, target)
			if target == "wasm32-wasi-http" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("missing serve import accepted")
			}
			message := diag.Format("handler.fern", source, err)
			if strings.Count(message, "error[E001]") != 1 || !strings.Contains(message, HandlerNeedsServe) {
				t.Fatalf("missing import: %s", message)
			}
			if hasMainDecl(prog) {
				t.Fatal("created a main without its serve dependency")
			}
		})
	}
	prog, err := parser.Parse(hostStubDecls + handler + "\nfunction main(): i32 { return 0; }")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatal(err)
	}
}
