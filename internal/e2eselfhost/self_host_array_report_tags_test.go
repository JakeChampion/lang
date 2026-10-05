package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pin every public reason constant to both its ordinal and printed tag. A
// table reorder must not silently change the meaning of a planner decision.
func TestSelfHostArrayReportReasonTags(t *testing.T) {
	groups := []struct {
		prefix, table string
		tags          []string
	}{
		{"fusion", "reasons", []string{
			"fused", "no-producer", "intermediate-shared", "control-flow-boundary",
			"effect-boundary", "element-fn-unresolved", "element-fn-effectful",
			"non-scalar", "result-escapes", "no-reduction-sink", "operator-outside-algebra", "disabled",
		}},
		{"storage", "storage_reasons", []string{
			"guarded-reuse", "reuse-disabled", "shape-unsupported", "borrow-linked-result",
			"receiver-not-consumed-param", "receiver-still-live", "receiver-supplied",
			"element-fn-unresolved", "element-fn-captures", "element-fn-effectful", "aliased-arguments",
		}},
		{"kernel", "kernel_reasons", []string{
			"scale-kernel", "disabled", "layout-unknown", "layout-strided", "layout-row-major",
			"element-fn-unresolved", "element-fn-captures", "element-not-literal-scale",
			"constructor-unavailable", "builtin-shadowed", "unsupported-operation", "unsupported-element-type",
			"outer-mul-kernel", "element-not-binary-multiply",
		}},
		{"scale_storage", "scale_storage_reasons", []string{
			"guarded-reuse", "reuse-disabled", "receiver-borrowed", "receiver-still-live",
			"receiver-supplied", "borrow-linked-result", "builtin-shadowed", "shape-unsupported",
		}},
	}
	var src strings.Builder
	src.WriteString("import \"./semarrayreport\";\nfunction main(): i32 {\n")
	for _, group := range groups {
		fmt.Fprintf(&src, "let %s: string[] = semarrayreport.%s();\n", group.prefix, group.table)
		fmt.Fprintf(&src, "if (%s.len() != %d) { return 1; }\n", group.prefix, len(group.tags))
		for ordinal, tag := range group.tags {
			call := "semarrayreport." + group.prefix + "_" + strings.ReplaceAll(tag, "-", "_") + "()"
			fmt.Fprintf(&src, "if (%s != %d || %s[%s] != %q) { print(%q); return 2; }\n",
				call, ordinal, group.prefix, call, tag, group.prefix+":"+tag)
		}
	}
	src.WriteString("return 0;\n}\n")
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "semarrayreport.fern")
	if err := os.WriteFile(filepath.Join(dir, "tags.fern"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "tags.fern", "tags")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("reason mapping: %v\n%s", err, out)
	}
}
