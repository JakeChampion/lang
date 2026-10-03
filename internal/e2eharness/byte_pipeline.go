package e2eharness

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

const BytePipelineProgram = `function main(): i32 {
  let r = stdin();
  let h = buf_new(8);
  while (true) {
    match (r.read_chunk_bytes(127)) {
      Err(_) => { buf_free(h); return 1; },
      Ok(chunk) => {
        if (chunk.len() == 0) { break; }
        buf_push_bytes_range(h, chunk, 0, chunk.len());
      }
    }
  }
  let result = buf_take_bytes(h);
  buf_free(h);
  let w = stdout();
  match (w.write_bytes(result)) {
    Some(_) => { return 2; },
    None => {},
  }
  return 0;
}
`

// CheckBytePipeline verifies the composed raw path, including using the
// extracted array after freeing its builder. Stderr remains separate from
// arbitrary output bytes so ownership diagnostics cannot look like output.
func CheckBytePipeline(t *testing.T, cmd *exec.Cmd, input []byte) string {
	t.Helper()
	cmd.Stdin = bytes.NewReader(input)
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	if err := cmd.Run(); err != nil {
		t.Fatalf("byte pipeline: %v\n%s", err, diagnostic.String())
	}
	if !bytes.Equal(out.Bytes(), input) {
		t.Fatalf("byte pipeline changed data: got %d bytes, want %d", out.Len(), len(input))
	}
	if strings.Contains(diagnostic.String(), "fern-sanitizer:") {
		t.Fatal(diagnostic.String())
	}
	return diagnostic.String()
}
