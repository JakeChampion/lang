package checker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestBuiltinQualifiedVariantPatterns(t *testing.T) {
	for _, enum := range builtinEnumDecls() {
		t.Run(enum.Name, func(t *testing.T) {
			typeName := enum.Name
			if len(enum.TypeParams) > 0 {
				typeName += "[" + strings.TrimSuffix(strings.Repeat("i32,", len(enum.TypeParams)), ",") + "]"
			}
			variant := enum.Variants[0]
			pattern := enum.Name + "." + variant.Name
			if len(variant.Payloads) > 0 {
				pattern += "(" + strings.TrimSuffix(strings.Repeat("_,", len(variant.Payloads)), ",") + ")"
			}
			for _, nested := range []bool{false, true} {
				scrutinee := "value"
				if nested {
					scrutinee = "Some(value)"
					pattern = "Some(" + pattern + ")"
				}
				source := fmt.Sprintf("function inspect(value: %s): i32 { return match (%s) { %s => 0, _ => 1 }; }", typeName, scrutinee, pattern)
				prog, _, err := modload.LoadSource(source)
				if err == nil {
					_, err = Check(prog)
				}
				if err != nil {
					t.Fatalf("nested=%v: %v\n%s", nested, err, source)
				}
			}
		})
	}
}

func TestBuiltinQualifiedPatternErrors(t *testing.T) {
	for _, tc := range []struct{ name, pattern, want string }{
		{"unknown module", "Missing.Interrupted", "unknown module"},
		{"unknown variant", "IoError.Missing", "is not part of enum IoError"},
		{"wrong enum", "StreamFault.StreamTimeout", "does not match scrutinee enum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "function inspect(value: IoError): i32 { return match (value) { " + tc.pattern + " => 0, _ => 1 }; }"
			prog, _, err := modload.LoadSource(source)
			if err == nil {
				_, err = Check(prog)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
