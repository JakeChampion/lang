package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"
)

func TestSelfHostWasmBufferRuntimeNeeds(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli, stdlib := witSelfHostCLI(t)
	cases := []struct {
		name, body string
		needs      []string
	}{
		{"empty", `buf_free(h); return 0;`, nil},
		{"length", `var n = buf_len(h); buf_free(h); return n;`, []string{"len"}},
		{"text", `buf_push(h, "hello"); var s = buf_take(h); buf_free(h); if (s != "hello") { return 1; } return 0;`, []string{"push", "reserve", "take"}},
		{"range", `buf_push_range(h, "hello", 1, 4); var s = buf_take(h); buf_free(h); if (s != "ell") { return 1; } return 0;`, []string{"push_range", "reserve", "take"}},
		{"byte-growth", `for i in 0..128 { buf_push_byte(h, 65); } var s = buf_take(h); buf_free(h); if (s.len() != 128 || s[127] != 65 as u8) { return 1; } return 0;`, []string{"push_byte", "reserve", "take"}},
		{"u64", `buf_push_u64(h, 123 as u64); var n = buf_len(h); buf_free(h); if (n != 8) { return 1; } return 0;`, []string{"push_u64", "reserve", "len"}},
		{"bytes", `var data: u8[] = [255 as u8, 128 as u8]; buf_push_bytes_range(h, data, 0, 2); var got = buf_take_bytes(h); buf_free(h); if (got.len() != 2 || got[0] != 255 as u8 || got[1] != 128 as u8) { return 1; } return 0;`, []string{"push_bytes_range", "take_bytes", "reserve"}},
		{"empty-bytes", `var got = buf_take_bytes(h); buf_free(h); return got.len();`, []string{"take_bytes"}},
	}
	for _, kind := range []string{"mapped", "filtered", "expanded"} {
		table, want := `[65 as u8]`, `"A"`
		if kind == "filtered" {
			table, want = `[1 as u8]`, `""`
		} else if kind == "expanded" {
			table = `[1 as u8, 65 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8]`
		}
		cases = append(cases, struct {
			name, body string
			needs      []string
		}{kind, `buf_push_` + kind + `(h, "\0", ` + table + `); var got = buf_take(h); buf_free(h); if (got != ` + want + `) { return 1; } return 0;`, []string{"push_" + kind, "reserve", "take"}})
	}
	definitions := regexp.MustCompile(`(?m)^  \(func \$__fern_buf_([A-Za-z0-9_]+)\b`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src, wat := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wat")
			if err := os.WriteFile(src, []byte(`function main(): i32 { var h: usize = buf_new(1); `+tc.body+` }`), 0o644); err != nil {
				t.Fatal(err)
			}
			compile := exec.Command(cli, "-target", "wasm32-wasi", "-emit", "asm", "-o", wat, src, stdlib)
			compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			text, err := os.ReadFile(wat)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, match := range definitions.FindAllStringSubmatch(string(text), -1) {
				got = append(got, match[1])
			}
			want := append([]string{"new", "free"}, tc.needs...)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("buffer helpers: got %v, want %v", got, want)
			}
			out, err := exec.Command(wasmtime, "run", wat).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
			// Each helper is also guest-local in a Preview 2 component.
			// Recording its individual need must not require a host import.
			component := filepath.Join(dir, "main.wasm")
			compile = exec.Command(cli, "-target", "wasm32-wasi", "-o", component, src, stdlib)
			compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("component compile: %v\n%s", err, out)
			}
			out, err = exec.Command(wasmtime, "run", component).CombinedOutput()
			if err != nil {
				t.Fatalf("component run: %v\n%s", err, out)
			}
			// Component entry points do not print the allocation census;
			// the core-module run above checks the same helper ownership.
		})
	}
}
