package componenttype

import "fmt"

// decode.go is the first slice of the "bring-your-own WIT" decoder
// (docs/WIT-BRING-YOUR-OWN.md, P1). A `component-type` payload — the bytes
// PayloadFor returns, and what `wasm-tools component embed` writes — is
// itself a full *component binary*: an 8-byte component header followed by
// component sections. For the production worlds those sections are a
// `wit-component-encoding` custom section, one component **type** section
// (id 7) whose single type is `Component([...])` describing the world, a
// component **export** section (id 11) naming the world, and a `producers`
// custom section.
//
// This file walks that top-level section framing. Decoding the type
// section's body (the component-model type grammar: instance/func/defined
// types, imports, exports, aliases) is the next slice; here we only split
// the payload into sections so later slices can reach for the type section
// without re-deriving the header / section offsets.

// Component section ids used in a component-type payload. The component
// binary format reuses core section-id numbering for the shared ids and
// adds component-specific ones; only the handful that appear in a
// `component-type` payload are named here.
const (
	secCustom = 0x00 // custom section (name + opaque payload)
	secType   = 0x07 // component type section
	secExport = 0x0b // component export section
)

// componentHeader is "\0asm" + component version 13, layer 1 — distinct
// from a core module's "\0asm" + version 1 (`01 00 00 00`).
var componentHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x0d, 0x00, 0x01, 0x00}

// Section is one top-level section of a component-type payload. Name is set
// only for custom sections (id 0); Body is the section contents (after the
// id and size, and — for custom sections — after the name).
type Section struct {
	ID   byte
	Name string // custom sections only
	Body []byte
}

// DecodeSections splits the `component-type` payload for `world` into its
// top-level component sections. Convenience wrapper over PayloadFor +
// SplitComponentSections.
func DecodeSections(world string) ([]Section, error) {
	payload, err := payloadFor(world)
	if err != nil {
		return nil, err
	}
	return SplitComponentSections(payload)
}

// SplitComponentSections validates the component header of a component-type
// payload and returns its top-level sections in order. The section bodies
// reference (do not copy) the input slice.
func SplitComponentSections(payload []byte) ([]Section, error) {
	if len(payload) < len(componentHeader) {
		return nil, fmt.Errorf("componenttype: payload too short (%d bytes) for a component header", len(payload))
	}
	for i, b := range componentHeader {
		if payload[i] != b {
			return nil, fmt.Errorf("componenttype: bad component header: got % x, want % x", payload[:len(componentHeader)], componentHeader)
		}
	}
	pos := len(componentHeader)
	var secs []Section
	for pos < len(payload) {
		id := payload[pos]
		pos++
		size, n, err := readULEB(payload[pos:])
		if err != nil {
			return nil, fmt.Errorf("componenttype: section %d size: %w", len(secs), err)
		}
		pos += n
		end := pos + int(size)
		if size > uint64(len(payload)-pos) {
			return nil, fmt.Errorf("componenttype: section %d size %d overruns payload (%d bytes left)", len(secs), size, len(payload)-pos)
		}
		body := payload[pos:end]
		sec := Section{ID: id, Body: body}
		if id == secCustom {
			name, k, err := readName(body)
			if err != nil {
				return nil, fmt.Errorf("componenttype: custom section %d name: %w", len(secs), err)
			}
			sec.Name = name
			sec.Body = body[k:]
		}
		secs = append(secs, sec)
		pos = end
	}
	return secs, nil
}

// TypeSectionBody returns the body of the single component type section in a
// component-type payload — the bytes the next decoder slice will parse into
// the world model. Errors if there isn't exactly one type section.
func TypeSectionBody(world string) ([]byte, error) {
	secs, err := DecodeSections(world)
	if err != nil {
		return nil, err
	}
	var found []byte
	for _, s := range secs {
		if s.ID == secType {
			if found != nil {
				return nil, fmt.Errorf("componenttype: world %q has more than one type section", world)
			}
			found = s.Body
		}
	}
	if found == nil {
		return nil, fmt.Errorf("componenttype: world %q has no type section", world)
	}
	return found, nil
}

// readULEB reads an unsigned LEB128 from the front of b, returning the value
// and the number of bytes consumed.
func readULEB(b []byte) (uint64, int, error) {
	var v uint64
	var shift uint
	for i, c := range b {
		if i >= 10 {
			return 0, 0, fmt.Errorf("uleb128 too long")
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, fmt.Errorf("uleb128 truncated")
}

// readName reads a uleb-prefixed UTF-8 name, returning it and the number of
// bytes consumed (uleb length + name bytes).
func readName(b []byte) (string, int, error) {
	n, k, err := readULEB(b)
	if err != nil {
		return "", 0, err
	}
	if n > uint64(len(b)-k) {
		return "", 0, fmt.Errorf("name length %d overruns %d bytes", n, len(b)-k)
	}
	return string(b[k : k+int(n)]), k + int(n), nil
}
