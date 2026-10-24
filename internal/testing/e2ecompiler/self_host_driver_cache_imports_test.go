package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostDriverCacheTracksSameLineImports(t *testing.T) {
	runner := x86_64Runner(t)
	dir := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cache_probe.fern", `import "./first"; import "./second";
function main(): i32 { print(first.prefix() + second.value()); return 0; }`)
	write("first.fern", `pub function prefix(): string { return "v"; }`)
	write("second.fern", `pub function value(): string { return "17"; }`)
	check := func(stem, want string) {
		t.Helper()
		driver := buildSelfHostBinFor(t, dir, "cache_probe.fern", stem, "x86-64-linux")
		output, err := runX86_64Bin(runner, driver).CombinedOutput()
		if err != nil {
			t.Fatalf("driver execution: %v\n%s", err, output)
		}
		if string(output) != want {
			t.Fatalf("driver output = %q, want %q; stale dependency cache", output, want)
		}
	}
	check("cache_before", "v17\n")
	write("second.fern", `pub function value(): string { return "29"; }`)
	check("cache_after", "v29\n")
}
