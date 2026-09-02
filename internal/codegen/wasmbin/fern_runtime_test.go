package wasmbin

import (
	"bytes"
	"testing"
)

// A runtime helper written in Fern (internal/fernrt) is a function of the
// module like the hand-built helpers, and like them it is private: no
// export carries its name.
func TestFernRuntimeHelperIsPrivate(t *testing.T) {
	wasm, err := buildFromSource(t, `function main(): i32 {
    return match (read_file("x")) { Ok(s) => s.len(), Err(_) => 1 };
}`)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wasm, []byte("__fern_utf8_valid")) {
		t.Error("__fern_utf8_valid is exported")
	}
	if !bytes.Contains(wasm, []byte("main")) {
		t.Error("main is not exported")
	}
}
