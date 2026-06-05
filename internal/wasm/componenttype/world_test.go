package componenttype

import (
	"bytes"
	"testing"
)

// TestDecodeWorldRoundTrip is P1's oracle: decoding the type and export
// sections of each shipped world and re-encoding them must reproduce the
// original section bodies byte-for-byte. That proves the whole nested type
// grammar (component/instance types, decls, externdescs, aliases, value
// types) decodes losslessly.
func TestDecodeWorldRoundTrip(t *testing.T) {
	for _, world := range []string{"fern", "http"} {
		t.Run(world, func(t *testing.T) {
			secs, err := DecodeSections(world)
			if err != nil {
				t.Fatalf("DecodeSections: %v", err)
			}
			w, err := DecodeWorld(world)
			if err != nil {
				t.Fatalf("DecodeWorld: %v", err)
			}
			for _, s := range secs {
				switch s.ID {
				case secType:
					got := encodeTypeSection(w.Types)
					if !bytes.Equal(got, s.Body) {
						t.Fatalf("%s type section re-encode mismatch (%d vs %d bytes)%s",
							world, len(got), len(s.Body), firstDiff(got, s.Body))
					}
				case secExport:
					got := encodeExportSection(w.Exports)
					if !bytes.Equal(got, s.Body) {
						t.Fatalf("%s export section re-encode mismatch (%d vs %d bytes)%s",
							world, len(got), len(s.Body), firstDiff(got, s.Body))
					}
				}
			}
		})
	}
}

// TestWorldImports checks the decoder surfaces the fern world's import
// interface names (what P2 will classify), in order.
func TestWorldImports(t *testing.T) {
	w, err := DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	got := worldImportNames(w)
	want := []string{
		"wasi:io/error@0.2.0",
		"wasi:io/streams@0.2.0",
		"wasi:cli/stdin@0.2.0",
		"wasi:cli/stdout@0.2.0",
		"wasi:cli/stderr@0.2.0",
		"wasi:io/poll@0.2.0",
		"wasi:clocks/wall-clock@0.2.0",
		"wasi:filesystem/types@0.2.0",
		"wasi:filesystem/preopens@0.2.0",
		"wasi:sockets/network@0.2.0",
		"wasi:sockets/instance-network@0.2.0",
		"wasi:sockets/tcp@0.2.0",
		"wasi:sockets/tcp-create-socket@0.2.0",
		"wasi:random/random@0.2.0",
	}
	if len(got) != len(want) {
		t.Fatalf("import count = %d, want %d\n got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("import %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// worldImportNames pulls the import-decl names out of the inner world
// component (Types[0] is the outer wrapper component; its first type decl is
// the world component whose import decls name the interfaces).
func worldImportNames(w *World) []string {
	if len(w.Types) == 0 || w.Types[0].Tag != tagComponent {
		return nil
	}
	for _, d := range w.Types[0].Decls {
		if d.Kind == 0x01 && d.Type != nil && d.Type.Tag == tagComponent {
			var names []string
			for _, inner := range d.Type.Decls {
				if inner.Kind == 0x03 { // import
					names = append(names, inner.Name)
				}
			}
			return names
		}
	}
	return nil
}

func firstDiff(a, b []byte) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := i - 4
			if lo < 0 {
				lo = 0
			}
			return "\n  first diff at " + itoa(i) +
				": got % " + hexx(a[lo:i+4]) + " want % " + hexx(b[lo:i+4])
		}
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func hexx(b []byte) string {
	const h = "0123456789abcdef"
	var s []byte
	for _, c := range b {
		s = append(s, h[c>>4], h[c&0xf], ' ')
	}
	return string(s)
}
