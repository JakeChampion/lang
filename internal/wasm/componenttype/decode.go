package componenttype

import "fmt"

const (
	secCustom = 0x00
	secType   = 0x07
	secExport = 0x0b
)

var componentHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x0d, 0x00, 0x01, 0x00}

type Section struct {
	ID   byte
	Name string
	Body []byte
}

func DecodeSections(world string) ([]Section, error) {
	payload, err := payloadFor(world)
	if err != nil {
		return nil, err
	}
	return SplitComponentSections(payload)
}

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
