package arm64

import (
	"strings"
	"testing"
)

// Both public entry points must reach the shared complete-line allocator,
// whose payload initialization and RC headers are audited separately.
func TestReadLineEntryPointsUseSharedLoop(t *testing.T) {
	g := &generator{stringLabel: map[string]string{}}
	g.emitReadLineRuntime()
	g.emitReaderWriterRuntime()
	asm := g.out.String()
	for _, p := range []struct{ name, fd string }{
		{"__fern_read_line", "mov w0, #0"},
		{"__fern_reader_read_line", "ldr w0, [x0]"},
	} {
		body := helperBody(asm, p.name)
		if !strings.Contains(body, p.fd) || !strings.Contains(body, "b __fern_read_line_fd") {
			t.Errorf("%s must pass its fd to the shared complete-line loop:\n%s", p.name, body)
		}
	}
}
