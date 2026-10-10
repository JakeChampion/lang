package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestConfigGetDiagnosticName(t *testing.T) {
	for _, target := range []string{"", "x86-64-linux", "wasm32-wasi", "wasm32-wasi-http"} {
		for _, tc := range []struct{ call, want string }{
			{`config_get(1)`, `argument 1 to "config_get": expected string, got i32`},
			{`config_get()`, `function "config_get"`},
			{`config_get("a", "b")`, `function "config_get"`},
		} {
			t.Run(target+"/"+tc.call, func(t *testing.T) {
				prog, err := parser.Parse("function main(): i32 { " + tc.call + "; return 0; }")
				if err != nil {
					t.Fatal(err)
				}
				_, err = CheckTarget(prog, target)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("diagnostic = %v, want %q", err, tc.want)
				}
			})
		}
	}
}

func TestDemangleModuleSeparator(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"", ""},
		{"P", "P"},
		{"module__Type", "module.Type"},
		{"module__Type__method", "module.Type__method"},
		{"__arr_push_shared_bytes", "__arr_push_shared_bytes"},
		{"__method_P_eq", "__method_P_eq"},
		{"_module__Type", "_module.Type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := demangle(tc.name); got != tc.want {
				t.Errorf("demangle(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
