# age

A Fern port of [age](https://age-encryption.org) 1.1.1, which encrypts
files in the age v1 format. This port has the X25519 recipients and
identities of age, the binary format and the ASCII armor.

```
$ age -r age1... -o file.age file          # encrypt to a recipient
$ age -r age1... -r age1... file > f.age   # encrypt to two recipients
$ age -a -R recipients.txt file > f.asc    # encrypt to the ASCII armor
$ age -e -i key.txt file > f.age           # encrypt to the key of an identity file
$ age -d -i key.txt -o file file.age       # decrypt
$ age -d -i key.txt < file.age > file      # decrypt the standard input
```

## Build and run

```
$ fern -O -target x86-64-linux -o age examples/age/age.fern   # or arm64-linux
```

Use `age-keygen` from age to make a key pair. This port has no key
generator yet.

## Options

| Option | Long form | Effect |
| --- | --- | --- |
| `-e` | `--encrypt` | Encrypt. This is the default. |
| `-d` | `--decrypt` | Decrypt. A file in the armor decrypts with no option. |
| `-o PATH` | `--output PATH` | Write the result to PATH. `-` is the standard output. |
| `-a` | `--armor` | Encrypt to the PEM-like ASCII armor. |
| `-r KEY` | `--recipient KEY` | Encrypt to the `age1...` public key KEY. Can be repeated. |
| `-R PATH` | `--recipients-file PATH` | Encrypt to the recipients in the file at PATH. Can be repeated. |
| `-i PATH` | `--identity PATH` | Use the secret keys in the file at PATH. Can be repeated. |

The flags follow the rules of the Go `flag` package, as in age. The input
file comes after the flags. With no input file, or with `-`, the port reads
the standard input. An identity file holds `AGE-SECRET-KEY-1...` lines.
Empty lines and lines that start with `#` are comments.

## Files

| File | Contents |
| --- | --- |
| `age.fern` | The command, the input and the output. |
| `cli.fern` | The flags and the checks that age makes before it reads the input. |
| `keys.fern` | Recipients, recipients files and identity files. |
| `bech32.fern` | Bech32, and a constant-time decoder for a secret key line. |
| `format.fern` | The header, its MAC, the X25519 stanza and the STREAM chunks. |
| `source.fern` | The input as Go's `bufio` and armor readers read it. |
| `text.fern` | Go's `%q` quotation, Go's strict base64 decoder and Go's names for system errors. |
| `age_test.fern` | The unit tests. |
| `testdata/cctv` | The C2SP CCTV age test vectors (`licenses/age-cctv`). |

## Compatibility

Files that age encrypts decrypt with this port. Files that this port
encrypts decrypt with age. The messages and the exit codes are those of
age 1.1.1:

- `age: error: ...` and exit 1 for a failure. A hint follows on lines that
  start with `age: hint: `.

- A bad flag gives the message of the Go `flag` package, the usage and
  exit 2.

age adds a last line to each error, which tells the user to report the
error to the age project. The port does not write that line. The usage
text is the text of the port. `--version` prints `v1.1.1-fern`.

### Differences from age 1.1.1

**The output file.** With `-o PATH`, the port writes the result to a
temporary file beside PATH. The name of the temporary file starts with
`.age-`. The temporary file takes the name PATH only after all of the input
decrypts. If a chunk does not authenticate, the port removes the temporary
file. A file that was at PATH then stays as it was.

age 1.1.1 writes each chunk to PATH when that chunk authenticates. Thus a
failure leaves part of the plaintext in PATH. When PATH is there and is not
a regular file, for example `/dev/null`, the port writes to it directly, as
age does.

**The standard output.** The port writes each chunk of plaintext to the
standard output when that chunk authenticates, as age does. If a later
chunk fails, the exit code is 1, and the chunks before it are on the
standard output. Each of those chunks is authentic, but the file is not
complete. Use `-o` for no partial plaintext.

**The armor.** The port follows the age specification where age 1.1.1 does
not. It refuses a carriage return in an armor line, other than one at the
end of the line. It refuses an empty line before the last line of the
armor. It accepts white space before the armor header. The CCTV vectors
`armor_cr_*`, `armor_double_crlf`, `armor_empty_last_line` and
`armor_whitespace_outside` test this.

**scrypt.** A header that has an `scrypt` stanza and another stanza gives
`an scrypt recipient must be the only one`, as the specification requires.
age 1.1.1 tries the other stanzas.

### Not in this port

- Passphrases: `-p`, files with an `scrypt` stanza, and identity files that
  age encrypts with a passphrase. They give a message that says so.

- SSH keys, plugins (`-j`, `AGE-PLUGIN-...` and `age1name1...`), and the
  ML-KEM hybrid recipients (`AGE-SECRET-KEY-PQ-1...`).

- `age-keygen`.

- age buffers an armored file that a user types at a terminal. The port
  reads a terminal as it reads a file.

## Security

The port refuses each malformed header:

- A wrong version line.

- A stanza argument that is not 1 or more printable bytes.

- A body line that is not canonical base64 with no padding, in 64 columns.

- A missing or malformed MAC line, and a MAC that does not verify.

The port refuses each malformed payload:

- A short chunk before the last chunk.

- An empty last chunk after other chunks.

- A missing last chunk, and a chunk that does not authenticate.

- Bytes after the last chunk.

The secret values go through the constant-time gate of the repository:
`__ct_secret` and `__ct_public` (`docs/NET-P4-TLS-PLAN.md`).

- In an identity file, the line structure, the comments and the
  `AGE-SECRET-KEY-1` prefix are not secret. The data part of each key line
  is secret. `bech32.decode_secret` decodes it with no branch and no index
  on its bytes. Only its verdict is public.

- For a key that is not valid, the port makes the line public. Then it
  gives the message of age, which can name the position of a bad
  character.

- The file key, the ephemeral scalars, the shared secrets and the keys
  from HKDF-SHA256 are secret. The MAC check and each tag check make only
  the verdict public. The ciphertext and the plaintext become public before
  the port writes them.

`TestSelfHostCtGateX86_64/examples/age` runs the port under memcheck, and
`TestSelfHostCtGateArm64/examples/age` does the same on an arm64 host. They
encrypt, decrypt, read the armor, try a key that does not match and read a
malformed key. A branch, an index or a write that depends on a secret byte
fails the test.

## Tests

`age_test.fern` holds the unit tests. `TestSelfHostExampleAgeUnit` runs it
on x86-64, arm64 and wasm32-wasi with the leak census. It tests these
parts:

- Bech32 with the BIP 173 vectors, and the constant-time key decoder
  against the plain decoder.

- Identity files and recipients, with their refusals.

- The CCTV `x25519` vector: the header, the MAC, the unwrap and the
  payload.

- Each header and stanza refusal, with the message of age.

- The STREAM nonce and the last-chunk flag.

- The armor and its refusals, the flags and the checks.

`TestSelfHostExampleAgeCLI` runs the port on x86-64 and arm64 against
`/usr/bin/age` 1.1.1. The test fails when age is not installed. It makes
these checks:

- age encrypts files of 0, 1, 65535, 65536, 65537 and 1 MiB + 3 bytes,
  with and without `-a`. The port decrypts them.

- The port encrypts the same sizes to two recipients. age decrypts them
  with each key.

- Both tools refuse 8 types of change to a file, with the same message.
  Four changes are one bit: in the header, in the MAC, in a chunk and in the
  last tag. The other four are a short last chunk, no last chunk, an extra
  chunk and an extra byte.

- After each refusal, the port writes no output file. A file that was there
  stays as it was.

- The port gives the message and the exit code of age for 51 malformed
  files and 53 command lines.

`TestSelfHostExampleAgeCCTV` decrypts the 147 vectors of the C2SP CCTV age
suite on x86-64 and arm64. Each vector gives its expected result. The
plaintext before a failure has the hash of the vector.

41 vectors need scrypt or the ML-KEM hybrid recipients. The test holds
their names, and it fails when one of them gives its expected result.

Four armor vectors do not start with the armor header. The port reads them
as plain files and gives a header failure, as age does.

## Performance

The test file has 1 GiB from `/dev/urandom`. Each tool encrypts it to one
X25519 recipient and decrypts it with the identity. Each time is the best
of 3 runs. The host is a 4-core Intel Xeon at 2.10 GHz (x86-64) with
Linux. The port is built with `fern -O -target x86-64-linux`. rage is not
installed on the host, so it is not in the table.

| Operation | age 1.1.1 | Fern port | Port / age |
| --- | ---: | ---: | ---: |
| Encrypt, file to file (`-o`) | 1.37 s | 5.17 s | 3.8 |
| Decrypt, file to file (`-o`) | 1.98 s | 4.75 s | 2.4 |
| Encrypt to `/dev/null` | 0.62 s | 4.13 s | 6.7 |
| Decrypt to `/dev/null` | 1.05 s | 4.15 s | 4.0 |

The port is slower than age. The goal is to be faster than age and rage,
so the port does not meet it yet. These are the causes:

- **ChaCha20.** About 98% of the instructions of a decryption are in
  `std/crypto/chacha20poly1305` (callgrind, 8 MiB). The kernel seals at
  about 310 MB/s, and ChaCha20 alone is at about 345 MB/s. It computes one
  64-byte block at a time with scalar `u32` operations. It writes each
  keystream word with four byte updates. age uses the assembly of
  golang.org/x/crypto, which computes many blocks at a time with AVX2 or
  NEON.

- **Poly1305.** Poly1305 alone is at about 1.25 GB/s. After a vector
  ChaCha20, it becomes the limit.

- **I/O.** The port reads one sealed chunk and writes one plaintext chunk
  with one system call each, as age does. The file-to-file times include
  up to about 1 s of disk writes for each tool.

The fix is a ChaCha20 kernel builtin for each backend, as `__aes_ctr32` is
for AES-GCM (`docs/NET-P4-TLS-PLAN.md`). It must do 8 blocks for each step
with AVX2 on x86-64 and 4 blocks with NEON on arm64. wasm32-wasi keeps the
scalar code. Fern has no vector type with `u32` lanes, so the kernel must
be in the compiler. Then a Poly1305 with 64-bit limbs and the `mulx`
multiply can remove the next limit. This work is not in this slice.

This slice fixed one cause in the standard library. `array.slice` on
`u8[]` appended each byte by itself, and it used 46% of the instructions of
a decryption. The byte-copy pass of the compiler takes only a loop that
counts from 0, and `array.slice` now counts from 0.

With the fix, decryption to a file went from 8.3 s to 4.75 s.
Encryption went from 6.5 s to 5.17 s.
