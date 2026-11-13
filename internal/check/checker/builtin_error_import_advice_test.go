package checker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestBuiltinErrorMethodImportAdvice(t *testing.T) {
	for _, recv := range []string{"IoError", "StreamFault"} {
		for _, method := range []string{"message", "to_string"} {
			source := fmt.Sprintf("function probe(e: %s): string { return e.%s(); }\nfunction main(): i32 { return 0; }", recv, method)
			t.Run(recv+"/"+method+"/missing", func(t *testing.T) {
				err := checkModuleSource(t, source)
				want := fmt.Sprintf("no method %q on %s — add `import \"core/error\"`", method, recv)
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("want %q, got %v", want, err)
				}
				ds, ok := err.(diag.Errors)
				if !ok || len(ds) != 1 {
					t.Fatalf("want one diagnostic, got %v", err)
				}
				if d, ok := ds[0].(diag.Coded); !ok || d.Code() != "E043" {
					t.Fatalf("want E043, got %v", ds[0])
				}
			})
			for _, imported := range []string{`import "core/error";`, `import "core/error.fern" as faults;`, `import "core/cmp";`, `import "std/io";`} {
				t.Run(recv+"/"+method+"/"+imported, func(t *testing.T) {
					if err := checkModuleSource(t, imported+"\n"+source); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
		for _, method := range []string{"mesage", "does_not_exist"} {
			t.Run(recv+"/"+method, func(t *testing.T) {
				source := fmt.Sprintf("function probe(e: %s): string { return e.%s(); }\nfunction main(): i32 { return 0; }", recv, method)
				err := checkModuleSource(t, source)
				if err == nil {
					t.Fatal("want a missing method diagnostic")
				}
				if method == "mesage" {
					if !strings.Contains(err.Error(), "did you mean \"message\", which needs `import \"core/error\"`?") {
						t.Fatal(err)
					}
				} else if !strings.Contains(err.Error(), "no stdlib module declares one") || strings.Contains(err.Error(), "import") {
					t.Fatal(err)
				}
			})
		}
	}
}
