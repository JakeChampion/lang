package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
)

func TestSelfHostWasiHttpTargetLifecycleHooks(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	const imports = "import \"std/http\";\nimport \"std/platform\";\n"
	const handler = "function handle(req: HttpRequest, plat: platform.Platform): HttpResponse { return http.ok(\"ok\"); }\n"
	for _, tc := range []struct{ name, decl, hook string }{
		{"void init", "function init(): void { assert(false); }\n", "init"},
		{"platform init", "function init(plat: platform.Platform): void { plat.log(\"starting\"); }\n", "init"},
		{"shutdown", "function shutdown(reason: string): void { assert(false); }\n", "shutdown"},
		{"hook after handler", handler + "function init(): void {}\n", "init"},
		{"explicit main", "function init(): void {}\nfunction main(): i32 { init(); return 0; }\n", "init"},
		{"source order", "function shutdown(reason: string): void {}\nfunction init(): void {}\n", "shutdown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := imports + tc.decl
			if !strings.Contains(tc.decl, "function handle(") {
				src += handler
			}
			dir := t.TempDir()
			prog, out := filepath.Join(dir, "main.fern"), filepath.Join(dir, "handler.wasm")
			if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			line := strings.Count(src[:strings.Index(src, "function "+tc.hook+"(")], "\n") + 1
			want := fmt.Sprintf("%s:%d:1: error[E075]: %s\n", prog, line, fmt.Sprintf(checker.WasiHTTPLifecycleHook, tc.hook))
			for _, mode := range []string{"check", "component", "core-module"} {
				t.Run(mode, func(t *testing.T) {
					args := []string{"-target", "wasm32-wasi-http"}
					if mode == "check" {
						args = append(args, "-check")
					} else {
						args = append(args, "-o", out)
						if mode == "core-module" {
							args = append(args, "-emit", mode)
						}
					}
					cmd := exec.Command(cli, append(args, prog, stdlib)...)
					cmd.Env = append(os.Environ(), "FERN_DIAG_FORMAT=terse")
					msg, err := cmd.CombinedOutput()
					if err == nil || string(msg) != want {
						t.Fatalf("want failed compile with %q, got %v\n%s", want, err, msg)
					}
					if _, err := os.Stat(out); !os.IsNotExist(err) {
						t.Fatalf("refused handler produced an artifact: %v", err)
					}
				})
			}
		})
	}
	t.Run("methods and imported helpers", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "helper.fern"), []byte("pub function init(): void {}\npub function shutdown(): void {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		compileWasiHttp(t, dir, imports+"import \"./helper\";\n"+handler+"struct Helper { n: i32 }\nfunction (h: Helper) init(): void {}\nfunction (h: Helper) shutdown(): void {}\n", "valid.wasm")
	})
}
