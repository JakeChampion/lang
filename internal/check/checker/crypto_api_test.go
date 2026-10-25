package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestCryptoFamiliesShareOneCallingConvention(t *testing.T) {
	const imports = `import "std/crypto";
import "std/crypto/aes_gcm";
import "std/crypto/chacha20poly1305";
import "std/crypto/ed25519";
import "std/crypto/ecdsa";
import "std/crypto/rsa";
import "std/crypto/sha2";
`
	const b = `let b: [u8] = "abc".as_bytes(); `
	cases := []struct{ name, body, diagnostic string }{
		{"digest-streaming", b + `let d: u8[] = crypto.md5_new().update(b).finish(); let e: u8[] = crypto.sha3_384_new().update(b).finish(); let f: u8[] = crypto.blake2b_new(32).update(b).finish();`, ""},
		{"digest-one-shots", b + `let all: u8[][] = [crypto.md5(b), crypto.sha1(b), crypto.sha224(b), crypto.sha256(b), crypto.sha384(b), crypto.sha512(b), crypto.sm3(b), crypto.blake2b(b, 32), crypto.sha3_224(b), crypto.sha3_256(b), crypto.sha3_384(b), crypto.sha3_512(b)];`, ""},
		{"mac-and-kdf-name-their-hash", b + `let m: boolean = crypto.hmac_sha256_verify(b, b, crypto.hmac_sha256(b, b)); let p: boolean = crypto.pbkdf2_sha256_verify(b, b, 1, crypto.pbkdf2_sha256(b, b, 1, 32)); let k: u8[] = crypto.hkdf_sha256_expand(crypto.hkdf_sha256_extract(b, b), b, 32);`, ""},
		{"aead-keys", b + `let Ok(g) = aes_gcm.key(b) else { return 1; }; let Ok(c) = chacha20poly1305.key(b) else { return 1; }; let x: Result[u8[], aead.AeadError] = g.seal(b, b, b); let y: Result[u8[], aead.AeadError] = c.open(b, b, b); let z: Result[u8[], aead.AeadError] = c.chacha20_xor(b, 1 as u32, b);`, ""},
		{"signature-keys", b + `let Ok(sk) = ed25519.private_key(b) else { return 1; }; let Ok(pk) = ed25519.public_key(sk.public_point()) else { return 1; }; let ok: boolean = pk.verify(b, sk.sign(b)); let Ok(ek) = ecdsa.private_key(ecdsa.P256, b) else { return 1; }; let sig: Result[u8[], ecdsa.EcdsaError] = ek.sign(sha2.Sha256, b); let shared: Result[u8[], ecdsa.EcdsaError] = ek.ecdh(b);`, ""},
		{"rsa-verifies-on-the-key", b + `let Ok(k) = rsa.public_key(b, b) else { return 1; }; let ok: boolean = k.verify_pss(sha2.Sha256, b, b) || k.verify_pkcs1v15(sha2.Sha256, b, b);`, ""},
		{"final-bytes-removed", b + `crypto.sha256_new().update(b).final_bytes();`, `no field or method "final_bytes"`},
		{"final-hex-removed", b + `crypto.sha256_new().update(b).final_hex();`, `no field or method "final_hex"`},
		{"sha3-final-hex-removed", b + `crypto.sha3_256_new().update(b).final_hex();`, `no field or method "final_hex"`},
		{"digest-bytes-twin-removed", b + `crypto.sha256_bytes(b);`, "sha256_bytes"},
		{"digest-hex-twin-removed", b + `crypto.md5_hex(b);`, "md5_hex"},
		{"sha3-hex-twin-removed", b + `crypto.sha3_256_hex(b);`, "sha3_256_hex"},
		{"hmac-hex-twin-removed", b + `crypto.hmac_sha256_hex(b, b);`, "hmac_sha256_hex"},
		{"hmac-verify-hex-removed", b + `crypto.hmac_verify_hex(b, b, "00");`, "hmac_verify_hex"},
		{"pbkdf2-hex-twin-removed", b + `crypto.pbkdf2_sha256_hex(b, b, 1, 32);`, "pbkdf2_sha256_hex"},
		{"hkdf-hex-twin-removed", b + `crypto.hkdf_sha256_hex(b, b, b, 32);`, "hkdf_sha256_hex"},
		{"hkdf-unnamed-hash-removed", b + `crypto.hkdf_extract(b, b);`, "hkdf_extract"},
		{"aes-gcm-raw-key-seal-removed", b + `aes_gcm.seal(b, b, b, b);`, "seal"},
		{"chacha-raw-key-open-removed", b + `chacha20poly1305.open(b, b, b, b);`, "open"},
		{"ed25519-seed-sign-removed", b + `ed25519.sign(b, b);`, "sign"},
		{"ecdsa-free-verify-removed", b + `let Ok(k) = ecdsa.public_key(ecdsa.P256, b) else { return 1; }; ecdsa.verify(k, sha2.Sha256, b, b);`, "verify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := imports
			if strings.Contains(tc.body, "aead.") {
				src += "import \"std/crypto/aead\";\n"
			}
			prog, _, err := modload.LoadSource(src + "function main(): i32 { " + tc.body + " return 0; }")
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
