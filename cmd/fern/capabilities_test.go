package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCapsTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The report attributes stdlib usage to the calling package (app gets
// `net` via std/fetch → tcp_connect), gives a path dependency its own
// row (helper uses fs directly), and includes the dependency's
// capabilities in the caller's row too — the walk crosses package
// boundaries, mirroring the brief's enforcement rule. Golden output:
// deterministic, sorted by package name.
func TestCapabilitiesReportPathDep(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"app/main.fern": `import "std/fetch";
import "helper";
function main(): i32 {
  var body: string = fetch.fetch_get(fetch.ipv4(127, 0, 0, 1), 8080, "/");
  helper.save(body);
  return 0;
}`,
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern": `pub function save(s: string): void {
  write_file("/tmp/fern-caps-out.txt", s);
}`,
	})
	var out strings.Builder
	if err := runCapabilities(filepath.Join(root, "app", "main.fern"), &out); err != nil {
		t.Fatal(err)
	}
	want := "app  fs,net  (example: main → lib__save → write_file)\n" +
		"helper  fs  (example: lib__save → write_file)\n"
	if out.String() != want {
		t.Fatalf("report:\ngot  %q\nwant %q", out.String(), want)
	}
}

// No fern.toml anywhere: a single `(root)` row.
func TestCapabilitiesReportNoManifest(t *testing.T) {
	root := writeCapsTree(t, map[string]string{
		"main.fern": `function main(): i32 {
  sleep_ms(1);
  return 0;
}`,
	})
	var out strings.Builder
	if err := runCapabilities(filepath.Join(root, "main.fern"), &out); err != nil {
		t.Fatal(err)
	}
	want := "(root)  time  (example: main → sleep_ms)\n"
	if out.String() != want {
		t.Fatalf("report:\ngot  %q\nwant %q", out.String(), want)
	}
}
