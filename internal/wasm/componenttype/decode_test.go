package componenttype

import "testing"

// TestSplitComponentSections checks the section walker against both shipped
// worlds: a component-type payload is an inner component binary whose
// sections are a wit-component-encoding custom section, exactly one type
// section, exactly one export section, and a producers custom section — and
// the section bodies tile the whole payload with nothing left over.
func TestSplitComponentSections(t *testing.T) {
	for _, world := range []string{"fern", "http"} {
		t.Run(world, func(t *testing.T) {
			payload, err := PayloadFor(world)
			if err != nil {
				t.Fatalf("PayloadFor(%q): %v", world, err)
			}
			secs, err := SplitComponentSections(payload)
			if err != nil {
				t.Fatalf("SplitComponentSections(%q): %v", world, err)
			}

			var nType, nExport int
			haveEncoding, haveProducers := false, false
			for _, s := range secs {
				switch s.ID {
				case secType:
					nType++
				case secExport:
					nExport++
				case secCustom:
					switch s.Name {
					case "wit-component-encoding":
						haveEncoding = true
					case "producers":
						haveProducers = true
					}
				}
			}
			if nType != 1 {
				t.Errorf("%s: type sections = %d, want 1", world, nType)
			}
			if nExport != 1 {
				t.Errorf("%s: export sections = %d, want 1", world, nExport)
			}
			if !haveEncoding {
				t.Errorf("%s: missing wit-component-encoding custom section", world)
			}
			if !haveProducers {
				t.Errorf("%s: missing producers custom section", world)
			}

			// The walker must consume the whole payload: re-measuring each
			// section's wire size (id + uleb(size) + body, with the name folded
			// back in for customs) plus the header must equal len(payload).
			total := len(componentHeader)
			for _, s := range secs {
				bodyLen := len(s.Body)
				if s.ID == secCustom {
					bodyLen += ulebLen(uint64(len(s.Name))) + len(s.Name)
				}
				total += 1 + ulebLen(uint64(bodyLen)) + bodyLen
			}
			if total != len(payload) {
				t.Errorf("%s: sections cover %d bytes, payload is %d", world, total, len(payload))
			}
		})
	}
}

// TestTypeSectionBody returns the lone type section's body for each world,
// and it must be non-empty (it carries the world's Component([...]) type).
func TestTypeSectionBody(t *testing.T) {
	for _, world := range []string{"fern", "http"} {
		body, err := TypeSectionBody(world)
		if err != nil {
			t.Fatalf("TypeSectionBody(%q): %v", world, err)
		}
		if len(body) == 0 {
			t.Errorf("%s: empty type section body", world)
		}
	}
}

func TestSplitComponentSectionsRejectsBadHeader(t *testing.T) {
	// A core-module header (version 1) must be rejected — the payload is a
	// component (version 13, layer 1), not a core module.
	core := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if _, err := SplitComponentSections(core); err == nil {
		t.Fatal("expected error for core-module header, got nil")
	}
	if _, err := SplitComponentSections([]byte{0x00, 0x61}); err == nil {
		t.Fatal("expected error for short payload, got nil")
	}
}

func TestReadULEB(t *testing.T) {
	cases := []struct {
		in   []byte
		want uint64
		n    int
	}{
		{[]byte{0x00}, 0, 1},
		{[]byte{0x7f}, 127, 1},
		{[]byte{0x80, 0x01}, 128, 2},
		{[]byte{0xe5, 0x8e, 0x26}, 624485, 3},
	}
	for _, c := range cases {
		v, n, err := readULEB(c.in)
		if err != nil || v != c.want || n != c.n {
			t.Errorf("readULEB(% x) = (%d, %d, %v), want (%d, %d, nil)", c.in, v, n, err, c.want, c.n)
		}
	}
	if _, _, err := readULEB([]byte{0x80}); err == nil {
		t.Error("expected truncation error")
	}
}

func ulebLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}
