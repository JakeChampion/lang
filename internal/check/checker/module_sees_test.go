package checker

import "testing"

func TestStdlibModuleSeesOnlyItsImports(t *testing.T) {
	tz, tm, result := "stdlib://std/tz.fern.md", "stdlib://std/time.fern.md", "stdlib://std/result.fern.md"
	c := &checker{info: &Info{ModuleImports: map[string]map[string]bool{tz: {tm: true}}}}
	if !c.moduleSees(tz, tm) {
		t.Fatal("std/tz should see the std/time it imports")
	}
	if c.moduleSees(tz, result) {
		t.Fatal("std/tz should not see std/result, which it does not import")
	}
}
