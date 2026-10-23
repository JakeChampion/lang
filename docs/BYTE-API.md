# Byte inputs and outputs

Status: [reference]

Read-only byte inputs use `[u8]`. An owned `u8[]` lends its bytes when passed
to that parameter. Use `text.as_bytes()` to pass text without a copy.
Functions that produce bytes return an owned `u8[]`.

```fern
import "std/string";
import "std/hex" as hex;
import "std/crypto" as crypto;

function main(): i32 {
  let bytes: [u8] = "abc".as_bytes();
  let encoded: string = hex.hex_encode(bytes);
  let digest: u8[] = crypto.sha256_new().update(bytes).final_bytes();
  print(encoded);
  print(hex.hex_encode(digest));
  return 0;
}
```

The encoders, checksums, digests, HMACs, KDFs, compression codecs, protobuf
wire reader, UTF-8 constructors and TZif reader accept views. The returned
buffers and text own their storage. This also applies to results from a slice.
A constructor that stores an input buffer, such as `stream_from_bytes`,
stores an owned array value.

Hash states have one `update(bytes: [u8])` method. Replace `update_bytes` and
`update_array` calls with `update`. Use `update(text.as_bytes())` for text.

Use `hmac_sha256(key, message)` for a raw HMAC. Both arguments are views.
PBKDF2 also takes views for the password and salt. These inputs can contain
any byte value.

Output names describe their encoding: `final_bytes` and `final_hex`, or
`sha256_bytes` and `sha256_hex`, return different representations. The byte
form owns its array. The hex form owns its text.

`string.at`, `first` and `last` return `Option[u8]`. Byte searches, counts
and replacements take `u8`. Indices, lengths and counts use `i32`.
A byte stream's `read_byte` also returns `Option[u8]` with the advanced cursor.

`pad_start`, `pad_end` and `center` take one `char` as their fill. Width is
in bytes. A fill is repeated whole, so a multibyte character can leave
space unused. Use `pad_start_str` or `pad_end_str` to repeat a text pattern.

The checksum view readers select the string or packed-array
kernel from the view's storage. They do not copy the input. The UTF-8
constructors copy the borrowed bytes once into the result's own storage.
The compiler combines their temporary byte copy and string construction
before it plans ownership.
