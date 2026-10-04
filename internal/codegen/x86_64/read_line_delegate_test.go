package x86_64

import (
	"strings"
	"testing"
)

// Both public entry points must reach the shared complete-line allocator,
// whose payload initialization and RC headers are audited separately.
func TestReadLineEntryPointsUseSharedLoop(t *testing.T) {
	g := &generator{syscalls: map[int]bool{}}
	g.emitReadLineRuntime()
	g.emitReaderWriterRuntime()
	g.flushPeep()
	asm := g.out.String()
	for _, p := range []struct{ name, fd string }{
		{"__fern_read_line", "xor edi, edi"},
		{"__fern_reader_read_line", "mov edi, [rdi]"},
	} {
		body := helperBody(t, asm, p.name)
		if !strings.Contains(body, p.fd) || !strings.Contains(body, "jmp __fern_read_line_fd") {
			t.Errorf("%s must pass its fd to the shared complete-line loop:\n%s", p.name, body)
		}
	}
}
