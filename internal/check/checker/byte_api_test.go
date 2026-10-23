package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestByteAPIsUseViewsBytesAndCharacters(t *testing.T) {
	const imports = `import "std/string";
import "std/hex" as hex;
import "std/hash" as hash;
import "std/crypto" as crypto;
`
	cases := []struct{ name, body, diagnostic string }{
		{"borrowed-codec", `let bytes: [u8] = "abc".as_bytes(); let result: string = hex.hex_encode(bytes);`, ""},
		{"owned-codec", `let bytes: u8[] = [97 as u8]; let result: string = hex.hex_encode(bytes);`, ""},
		{"borrowed-update", `let h = crypto.sha256_new().update("abc".as_bytes()); let c = hash.cksum_new().update([97 as u8]);`, ""},
		{"single-byte-result", `let first: Option[u8] = "abc".first(); let last: Option[u8] = "abc".last(); let at: Option[u8] = "abc".at(1);`, ""},
		{"character-fill", `let result = "a".pad_start(4, 'é').pad_end(6, '中').center(10, '-');`, ""},
		{"text-needs-view", `hex.hex_encode("abc");`, "expected [u8], got string"},
		{"update-bytes-removed", `crypto.sha256_new().update_bytes([97 as u8]);`, `no field or method "update_bytes"`},
		{"update-array-removed", `hash.cksum_new().update_array([97 as u8]);`, `no field or method "update_array"`},
		{"hmac-duplicate-removed", `crypto.hmac_sha256_bytes([97 as u8], "abc");`, "hmac_sha256_bytes"},
		{"fill-is-character", `"a".pad_start(4, "é");`, "expected char, got string"},
		{"single-byte-input", `let wide: i32 = 255; "a".count_byte(wide);`, "expected u8, got i32"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(imports + "function main(): i32 { " + tc.body + " return 0; }")
			if err == nil {
				_, err = Check(prog)
			}
			if tc.diagnostic == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("got %v, want %q", err, tc.diagnostic)
			}
		})
	}
}
