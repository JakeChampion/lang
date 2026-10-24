package componenttype

import (
	_ "embed"
	"fmt"
)

//go:embed fern.bin
var fernPayload []byte

//go:embed http.bin
var httpPayload []byte

//go:embed proxy.bin
var proxyPayload []byte

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
	out = append(out, 0x00)
	out = appendULEB(out, uint64(len(body)))
	out = append(out, body...)
	return out, nil
}

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
	case "fern":
		return fernPayload, nil
	case "http":
		return httpPayload, nil
	case "proxy":
		return proxyPayload, nil
	default:
		return nil, fmt.Errorf("componenttype: unknown world %q (want \"fern\", \"http\" or \"proxy\")", world)
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
