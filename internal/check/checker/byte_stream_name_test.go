package checker

import "testing"

func TestByteStreamAndAsyncStreamAreDistinct(t *testing.T) {
	for _, source := range []string{
		`@import("test:dep/d", "body") async function body(): Stream[u8]; function f(): ByteStream { return ByteStream { data: [], pos: 0, source: None }; }`,
		`struct Stream { value: i32 } function f(): ByteStream { return ByteStream { data: [], pos: 0, source: None }; } function g(): Stream { return Stream { value: 7 }; }`,
		`function default(value: i32): Option[u8] { return value as? u8; }`,
	} {
		if err := checkSource(t, source); err != nil {
			t.Fatal(err)
		}
	}
}

func TestByteStreamHasNoGenericPayload(t *testing.T) {
	if err := checkSource(t, `function f(value: ByteStream[u8]): i32 { return 0; }`); err == nil {
		t.Fatal("accepted a generic byte reader")
	}
}
