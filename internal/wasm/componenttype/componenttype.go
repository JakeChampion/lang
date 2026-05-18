// Package componenttype embeds the precomputed `component-type`
// custom-section payloads for the two production WIT worlds
// (`lang` and `http`) and exposes a function that appends one
// to a core wasm module — the same operation `wasm-tools
// component embed -w <world>` performs.
//
// The payload bytes are deterministic per world and independent
// of the core module they're attached to: `wasm-tools embed`
// reads no information from the core module beyond "module
// header is valid", so we can hand-roll the section without
// linking against wasm-tools.
//
// Regeneration: see doc.go for the exact wasm-tools invocation
// that produced lang.bin / http.bin from cmd/lang/wit/.
package componenttype

import (
	_ "embed"
	"fmt"
)

//go:embed lang.bin
var langPayload []byte

//go:embed http.bin
var httpPayload []byte

// Embed appends the `component-type` custom section for `world`
// to `core` and returns the concatenated bytes. `world` must
// be "lang" or "http"; any other value returns an error. `core`
// is not modified.
//
// Custom section wire format
// (https://webassembly.github.io/spec/core/binary/modules.html#binary-customsec):
//   id (0x00)
//   size : uleb128       — over name-len + name + payload
//   name-len : uleb128   — 14 (len of "component-type")
//   name : 14 bytes      — "component-type"
//   payload : N bytes    — world-specific, precomputed
func Embed(core []byte, world string) ([]byte, error) {
	payload, err := payloadFor(world)
	if err != nil {
		return nil, err
	}
	name := []byte("component-type")
	body := make([]byte, 0, 1+len(name)+len(payload))
	body = appendULEB(body, uint64(len(name)))
	body = append(body, name...)
	body = append(body, payload...)

	out := make([]byte, 0, len(core)+1+5+len(body))
	out = append(out, core...)
	out = append(out, 0x00) // custom section id
	out = appendULEB(out, uint64(len(body)))
	out = append(out, body...)
	return out, nil
}

// PayloadFor returns the precomputed payload for `world` —
// primarily exported for tests that want to assert byte-level
// equivalence with the wasm-tools output. Callers building a
// component should use Embed instead; PayloadFor returns the
// inner bytes only (no section header, no name).
func PayloadFor(world string) ([]byte, error) {
	p, err := payloadFor(world)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(p))
	copy(out, p)
	return out, nil
}

func payloadFor(world string) ([]byte, error) {
	switch world {
	case "lang":
		return langPayload, nil
	case "http":
		return httpPayload, nil
	default:
		return nil, fmt.Errorf("componenttype: unknown world %q (want \"lang\" or \"http\")", world)
	}
}

func appendULEB(buf []byte, v uint64) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(buf, b)
		}
		buf = append(buf, b|0x80)
	}
}
