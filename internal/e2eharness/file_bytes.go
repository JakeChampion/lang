package e2eharness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const FileBytesProgram = `import "std/array";
function main(): i32 {
  var payload: u8[] = [];
  var i: i32 = 0;
  while (i < 8193) { payload = payload.append((i % 256) as u8); i = i + 1; }
  match (write_file_bytes("all.bin", payload)) { Err(_) => { return 1; }, Ok(_) => {} }
  match (write_file_bytes("again.bin", payload)) { Err(_) => { return 2; }, Ok(_) => {} }
  if (payload.len() != 8193 || payload[255] != 255 || payload[8192] != 0) { return 3; }
  var empty: u8[] = [];
  match (write_file_bytes("empty.bin", empty)) { Err(_) => { return 4; }, Ok(_) => {} }
  match (write_file_bytes("temporary.bin", [255 as u8, 0 as u8, 128 as u8])) { Err(_) => { return 5; }, Ok(_) => {} }
  match (write_file("mode-reference.bin", "reference")) { Err(_) => { return 12; }, Ok(_) => {} }
  match (write_file_bytes("absent/file.bin", payload)) { Ok(_) => { return 6; }, Err(e) => {
    match (e) { NotFound(p) => { if (p != "absent/file.bin") { return 7; } }, _ => { return 8; } }
  } }
  match (write_file_bytes("directory", payload)) { Ok(_) => { return 9; }, Err(_) => {} }
  match (write_file_bytes("victim\0suffix", payload)) { Ok(_) => { return 10; }, Err(_) => {} }
  if (payload[1] != 1 || payload[255] != 255) { return 11; }
  return 0;
}
`

// CheckFileBytes uses host reads as the oracle, independent of Fern's readers.
func CheckFileBytes(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	dir := t.TempDir()
	cmd.Dir = dir
	for _, name := range []string{"all.bin", "empty.bin", "victim"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("retained"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	if err := cmd.Run(); err != nil {
		t.Fatalf("file byte sink: %v\n%s\n%s", err, out.String(), diagnostic.String())
	}
	want := make([]byte, 8193)
	for i := range want {
		want[i] = byte(i)
	}
	for name, expected := range map[string][]byte{
		"all.bin": want, "again.bin": want, "empty.bin": {},
		"temporary.bin": {255, 0, 128}, "victim": []byte("retained"),
		"mode-reference.bin": []byte("reference"),
	} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, expected) {
			t.Fatalf("%s: got %d bytes, want exact %d-byte payload", name, len(got), len(expected))
		}
	}
	info, err := os.Stat(filepath.Join(dir, "all.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("existing mode changed: %v", info.Mode())
	}
	created, err := os.Stat(filepath.Join(dir, "temporary.bin"))
	if err != nil {
		t.Fatal(err)
	}
	reference, err := os.Stat(filepath.Join(dir, "mode-reference.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if created.Mode().Perm() != reference.Mode().Perm() {
		t.Fatalf("creation mode differs from write_file: bytes=%v text=%v", created.Mode(), reference.Mode())
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", out.Bytes())
	}
	return diagnostic.String()
}
