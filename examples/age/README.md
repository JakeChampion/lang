# age

A Fern port of [age](https://age-encryption.org) 1.3.2 and of its key
generator, age-keygen. age encrypts files in the age v1 format. The port
has these parts of age: X25519 keys, ML-KEM-768 + X25519 hybrid keys,
passphrases, SSH keys, the binary format and the ASCII armor.

```
$ age -r age1... -o file.age file          # encrypt to a recipient
$ age -r age1pq1... -o file.age file       # encrypt to a hybrid recipient
$ age -r age1... -r age1... file > f.age   # encrypt to two recipients
$ age -a -R recipients.txt file > f.asc    # encrypt to the ASCII armor
$ age -p -o file.age file                  # encrypt with a passphrase
$ age -R ~/.ssh/id_ed25519.pub file > f.age # encrypt to an SSH key
$ age -e -i key.txt file > f.age           # encrypt to the key of an identity file
$ age -d -i key.txt -o file file.age       # decrypt
$ age -d -i ~/.ssh/id_rsa file.age > file  # decrypt with an SSH key
$ age -d file.age > file                   # decrypt with a passphrase
$ age-keygen -o key.txt                    # make a key pair
$ age-keygen -pq -o key.txt                # make a hybrid key pair
$ age-keygen -y key.txt                    # write the recipient of key.txt
```

## Build and run

```
$ fern -O -target x86-64-linux -o age examples/age/age.fern          # or arm64-linux
$ fern -O -target x86-64-linux -o age-keygen examples/age/keygen.fern
```

The terminal code uses the termios flags of Linux, so the port runs on
Linux.

## Options

| Option | Long form | Effect |
| --- | --- | --- |
| `-e` | `--encrypt` | Encrypt. This is the default. |
| `-d` | `--decrypt` | Decrypt. A file in the armor decrypts with no option. |
| `-o PATH` | `--output PATH` | Write the result to PATH. `-` is the standard output. |
| `-a` | `--armor` | Encrypt to the PEM-like ASCII armor. |
| `-p` | `--passphrase` | Encrypt with a passphrase. |
| `-r KEY` | `--recipient KEY` | Encrypt to KEY: an `age1...` or `age1pq1...` public key, or an SSH public key. Can be repeated. |
| `-R PATH` | `--recipients-file PATH` | Encrypt to the recipients in the file at PATH. Can be repeated. |
| `-i PATH` | `--identity PATH` | Use the secret keys in the file at PATH. Can be repeated. |

The flags follow the rules of the Go `flag` package, as in age. The input
file comes after the flags. With no input file, or with `-`, the port reads
the standard input.

An identity file holds `AGE-SECRET-KEY-1...` and `AGE-SECRET-KEY-PQ-1...`
lines, or one SSH private key. Empty lines and lines that start with `#`
are comments. An age file that a passphrase encrypts can also be an
identity file. Then the port asks for the passphrase when it needs the
keys.

As age does, the port warns about a recipient, a recipients file or an
identity file that the command line names two times. It refuses an
output file that is also the input, an identity file or a recipients
file. A decrypted file goes to a terminal only when it is text: valid
UTF-8 with no control character other than a line feed, a carriage return
or a tab.

`age-keygen` has `-pq`, `-o PATH` and `-y`. Without `-y`, it makes a key
pair: an X25519 key pair, or with `-pq` a hybrid key pair. Its
`# created:` line has the local time in the RFC 3339 format. When the
output is not a terminal, it writes `Public key: age1...` to the standard
error. With `-o`, it makes a new file with mode 0600.

With `-y`, `age-keygen` writes the recipient of each key of an identity
file.

### Hybrid keys

A hybrid recipient (`age1pq1...`, 1959 characters) holds an ML-KEM-768
public key and an X25519 public key. Its stanza is `mlkem768x25519`: HPKE
(RFC 9180) with the MLKEM768-X25519 KEM of draft-ietf-hpke-pq, HKDF-SHA256
and ChaCha20Poly1305, as age 1.3 specifies it. A hybrid identity
(`AGE-SECRET-KEY-PQ-1...`) holds the 32-byte seed of both keys. The module
`std/crypto/hpke` does the work.

A file to a hybrid recipient must not have a recipient that is not
hybrid, because then a quantum computer can decrypt it. The port refuses
that mix with the message of age:

```
age: error: incompatible recipients: can't mix post-quantum and classic recipients, or the file would be vulnerable to quantum computers
```

### Passphrases

The port asks for a passphrase on the terminal, as age does: it opens
`/dev/tty`, or it uses the standard input when that is a terminal. The
terminal does not echo the passphrase. With no terminal, the message is
that of age:

```
age: error: could not read passphrase: standard input is not a terminal, and /dev/tty is not available: open /dev/tty: no such device or address
```

`age -p` asks twice. For an empty passphrase, it makes a passphrase of 10
random words from the BIP 39 list. It shows that passphrase on the
terminal. The scrypt work factor is 2^18. A file with a work factor of
more than 2^22 gives `scrypt work factor too large`. `age -d` with no `-i` asks for a
passphrase only for a file that has one scrypt stanza.

### SSH keys

A recipient can be an `ssh-ed25519` or an `ssh-rsa` public key of 2048
bits or more, in the `authorized_keys` format. In a recipients file, the
port ignores an SSH key of another type, and it shows the message of age.

An identity file can hold an SSH private key in these formats:

- OpenSSH (`-----BEGIN OPENSSH PRIVATE KEY-----`), with or without a
  passphrase. A key with a passphrase uses bcrypt and `aes256-ctr`, as
  `ssh-keygen` makes it. The port asks for the passphrase only when a
  stanza is for that key.

- PKCS#1 (`-----BEGIN RSA PRIVATE KEY-----`) and PKCS#8
  (`-----BEGIN PRIVATE KEY-----`) with no passphrase, for RSA and Ed25519.

## Files

| File | Contents |
| --- | --- |
| `age.fern` | The command, the input and the output. |
| `keygen.fern` | The `age-keygen` command. |
| `cli.fern` | The flags of age and the checks that age makes before it reads the input. |
| `goflag.fern` | Go's `flag` package, for both commands. |
| `keys.fern` | Recipients, recipients files and identity files. |
| `bech32.fern` | Bech32, and a constant-time decoder for a secret key line. |
| `format.fern` | The header, its MAC, the X25519, hybrid and scrypt stanzas and the STREAM chunks. |
| `ssh.fern` | SSH keys: the wire format, `authorized_keys` lines, private key files, and the `ssh-ed25519` and `ssh-rsa` stanzas. |
| `bcrypt.fern` | Blowfish, `bcrypt_pbkdf` and AES-256-CTR, for an OpenSSH key with a passphrase. |
| `tty.fern` | The terminal: prompts with no echo, and the messages for the user at the terminal. |
| `wordlist.fern` | The BIP 39 word list, for the passphrases that age makes. |
| `source.fern` | The input as Go's `bufio` and armor readers read it. |
| `text.fern` | Go's `%q` quotation, Go's base64 decoders and Go's names for system errors. |
| `age_test.fern` | The unit tests. |
| `testdata/cctv` | The C2SP CCTV age test vectors (`licenses/age-cctv`). |
| `testdata/ssh` | SSH test keys from Python's `cryptography` package. The passphrase of the encrypted keys is `ssh pass`. |

The port uses `std/crypto/hpke` for the hybrid stanza, `std/crypto/scrypt`,
the RSA-OAEP of `std/crypto/rsa` and the Montgomery form of
`std/crypto/ed25519`.

## Compatibility

Files that age encrypts decrypt with this port. Files that this port
encrypts decrypt with age. The messages and the exit codes are those of
age 1.3.2:

- `age: error: ...` and exit 1 for a failure. A hint follows on lines that
  start with `age: hint: `.

- A bad flag gives the message of the Go `flag` package, the usage and
  exit 2.

age adds a last line to each error, which tells the user to report the
error to the age project. The port does not write that line. The usage
text is the text of the port. `--version` prints `v1.3.2-fern`. The same
rules apply to `age-keygen`.

### Differences from age 1.3.2

**The output file.** With `-o PATH`, the port writes the result to a
temporary file beside PATH. The name of the temporary file starts with
`.age-`. The temporary file takes the name PATH only after all of the input
decrypts. If a chunk does not authenticate, the port removes the temporary
file. A file that was at PATH then stays as it was.

age 1.3.2 writes each chunk to PATH when that chunk authenticates. Thus a
failure leaves part of the plaintext in PATH. When PATH is there and is not
a regular file, for example `/dev/null`, the port writes to it directly, as
age does.

**The standard output.** The port writes each chunk of plaintext to the
standard output when that chunk authenticates, as age does. If a later
chunk fails, the exit code is 1, and the chunks before it are on the
standard output. Each of those chunks is authentic, but the file is not
complete. Use `-o` for no partial plaintext.

**scrypt.** A header that has an `scrypt` stanza and another stanza gives
`an scrypt recipient must be the only one`, as the specification requires.
age 1.3.2 tries the other stanzas when `-i` names identities. The CCTV
vector `scrypt_and_x25519` tests this.

**SSH keys.** These keys give a message of the port, where age gives the
message of Go's parser:

- A PEM private key with the `Proc-Type: 4,ENCRYPTED` header (the old
  format of `ssh-keygen -m PEM`). age decrypts it after the passphrase.
  The port encrypts to its `.pub` file, but it does not decrypt with it.

- An OpenSSH private key that uses the `aes256-cbc` cipher.

- An EC or a DSA private key. age parses it, then it refuses its type. The
  port refuses the type with no check of the key, so a malformed key gives
  the message for its type.

- An ECDSA, a security-key or a certificate public key. The port knows the
  type but does not check the key. A malformed key of one of these types
  gives `unknown SSH recipient type` where age gives the message of Go's
  parser for that type.

- An Ed25519 public key with a y value that is not canonical. Go's
  edwards25519 accepts it, and the port refuses it.

### Not in this port

- Plugins (`-j`, `AGE-PLUGIN-...` and `age1name1...`). They give a message
  that says so. The plugin protocol runs `age-plugin-NAME` with pipes, and
  Fern has `proc_fork`, `proc_exec`, `pipe` and `dup_onto` for that. The
  protocol has a state machine for each side, and a test needs a plugin.
  This slice does not have that work. The release archive of age 1.3.2
  also has the plugins `age-plugin-batchpass`, `age-plugin-pq`,
  `age-plugin-tag` and `age-plugin-tagpq`. The port does not have them.

- The tag recipients (`age1tag1...` and `age1tagpq1...`, the `p256tag` and
  `mlkem768p256tag` stanzas). age encrypts to them, and a plugin on a
  hardware token decrypts. They need HPKE with the P-256 KEM and the
  MLKEM768-P256 KEM. The port gives a message that says so.

- `age-inspect`, the command of age 1.3 that shows the header of a file.

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

- A passphrase is secret after the port reads its line. The check of the
  second passphrase makes only the verdict public. scrypt reads its memory
  at an index that depends on the passphrase. Thus `std/crypto/scrypt`
  makes that index public, as each scrypt must.

- The seed of an Ed25519 SSH key is secret. The primes of an RSA SSH key
  and the values of the CRT are secret. The RSA decryption uses the CRT, as
  Go does. It reads the exponents with masks, and then it checks that m^e
  is c again. That check and the OAEP check make only their verdicts
  public. The base64 of a PEM file and the checks of an RSA key are not
  constant-time, as in Go: the port marks the key secret after them. Blowfish, for an OpenSSH key with a
  passphrase, reads its tables at secret indexes, as OpenSSH does.

- The seed of a hybrid key, the ML-KEM-768 decapsulation key, the X25519
  scalars, the HPKE randomness and the HPKE shared secret are secret. The
  ML-KEM-768 decapsulation has no branch on the ciphertext: a wrong
  ciphertext gives a random secret, and then the tag check fails.

`TestSelfHostCtGateX86_64/examples/age` runs the port under memcheck, and
`TestSelfHostCtGateArm64/examples/age` does the same on an arm64 host. They
encrypt, decrypt, read the armor, try a key that does not match and read a
malformed key, with X25519 keys and with hybrid keys. They decrypt a CCTV
scrypt file with the right passphrase and with a wrong one. They encrypt
and decrypt with Ed25519 and RSA SSH keys.
A branch, an index or a write that depends on a secret byte fails the
test.

## Tests

`age_test.fern` holds the unit tests. `TestSelfHostExampleAgeUnit` runs it
on x86-64, arm64 and wasm32-wasi with the leak census. It tests these
parts:

- Bech32 with the BIP 173 vectors, and the constant-time key decoder
  against the plain decoder.

- Identity files and recipients, with their refusals, as age and as
  age-keygen read them.

- The CCTV `x25519` vector: the header, the MAC, the unwrap and the
  payload.

- The CCTV `hybrid` stanza: the unwrap and each refusal, a hybrid wrap and
  unwrap, and hybrid recipient strings with their refusals.

- Each header and stanza refusal, with the message of age.

- The scrypt stanza and each of its refusals.

- `authorized_keys` lines, an `ssh-ed25519` wrap and unwrap, and
  malformed SSH private key files.

- `bcrypt_pbkdf` and AES-256-CTR against Python. The counter of the CTR
  test carries over 32 bits.

- The STREAM nonce and the last-chunk flag.

- The armor and its refusals, the flags and the checks.

`TestSelfHostExampleAgeCLI` runs the port on x86-64 and arm64 against age
1.3.2 on the `PATH`. The test fails when age is not installed, or when
`age --version` is not `v1.3.2`. CI installs the release archive of age
from GitHub and checks its SHA-256 (`.github/actions/port-oracles`). The
test makes these checks:

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

- The port gives the message and the exit code of age for 63 malformed
  files and 63 command lines. The files include a header of 3 MiB, 1025
  stanzas and a stanza with 130 arguments. The command lines include
  repeated flags, an output file that is an input file, and keys in the
  wrong file.

- Hybrid keys. Each tool decrypts what the other encrypts to two hybrid
  recipients, with `-R` and `-r`, for files of 0, 1, 65536 and
  1 MiB + 3 bytes, with and without `-a`. age decrypts what the port
  encrypts with `-e -i`. For 27 command lines, the two tools give the same
  message: a mix of hybrid and classic recipients, malformed hybrid keys,
  and changed hybrid stanzas.

- Passphrases. Each tool gets a new pseudo-terminal, and the test types
  the passphrase after the prompt. Each tool decrypts what the other
  encrypts with `-p`, with and without `-a`. age decrypts with the
  passphrase that the port makes. For 15 command lines, the two tools give
  the same message, exit code and terminal text. These lines have wrong,
  empty and unequal passphrases, a Ctrl-D, and identity files that a
  passphrase encrypts. With no terminal, the two tools give the same
  message.

- SSH keys. Each tool decrypts what the other encrypts, with `-R` and
  `-r`, for Ed25519, RSA-2048 and RSA-3072 keys, and the port reads
  PKCS#1 and PKCS#8 keys. Both tools decrypt with OpenSSH keys that have a
  passphrase. For 35 command lines, the two tools give the same message:
  malformed keys and stanzas, a key that does not match, and keys of
  other types.

`TestSelfHostExampleAgeKeygen` runs the age-keygen port on x86-64 and
arm64 against age-keygen 1.3.2. Each key pair works with age, with and
without `-pq`. The `# created:` line has the time zone of `TZ`. `-o` makes
a file with mode 0600. Each tool gives the same recipients for the keys of
the other. For 22 command lines, the two tools give the same output and
message.

`TestSelfHostExampleAgeCCTV` decrypts the 147 vectors of the C2SP CCTV age
suite on x86-64 and arm64. Each vector gives its expected result. The
plaintext before a failure has the hash of the vector. A vector with a
passphrase runs on a pseudo-terminal. All 147 vectors pass, with the 20
hybrid vectors.

Four armor vectors do not start with the armor header. The port reads them
as plain files and gives a header failure, as age does.

## Performance

The input is 1 GiB of zeros in a sparse file. Each tool encrypts it to one
X25519 recipient, and decrypts the file that age made with the identity.
The output goes to `/dev/null`.

Each time is the best of 7 runs, with the two tools in turn. The host is a 4-core Intel Xeon (2.10 GHz, 3.2 GHz with
turbo) with Linux, and other jobs used it at the same time. The port is
built with `fern -O -target x86-64-linux`. rage is not installed on the
host, so it is not in the table.

| Operation | age 1.1.1 | Fern port | Port / age |
| --- | ---: | ---: | ---: |
| Encrypt to `/dev/null` | 0.58 s | 0.53 s | 0.91 |
| Decrypt to `/dev/null` | 1.02 s | 0.68 s | 0.67 |

The port encrypts and decrypts faster than age. The best CPU time (user and
system) of 7 encrypts is 0.52 s for the port and 0.65 s for age.

The port seals and opens each chunk in one kernel of the compiler,
`__fern_chacha20_poly1305_seal_into` or `__fern_chacha20_poly1305_open_into`.
The kernel computes ChaCha20 in the AVX2 registers, eight blocks at a time.
It computes Poly1305 in the general registers among the rounds of ChaCha20,
so the two run at the same time. The AVX2 assembly of golang.org/x/crypto,
which age uses, does the same. The kernel seals at about 1.1 cycles for each
byte. Before the fused kernel, the port computed ChaCha20 and then Poly1305
at about 1.6 cycles for each byte. Then it encrypted in 0.63 s on the same
host.

Two copies remain in the encrypt path of the port: the copy of `read` in the
kernel, and the copy that adds the room for the tag to each chunk.

### Passphrases and SSH keys

Each time is the best of 3 runs on the same host. A run with a passphrase
includes about 0.03 s for the Python program that drives the
pseudo-terminal. Other jobs ran on the host at the same time.

| Operation | age 1.1.1 | Fern port | Port / age |
| --- | ---: | ---: | ---: |
| Decrypt, scrypt work factor 2^18 | 0.80 s | 0.89 s | 1.1 |
| Encrypt, scrypt work factor 2^18 | 0.77 s | 0.98 s | 1.3 |
| Decrypt with an Ed25519 SSH key | 0.005 s | 0.003 s | 0.6 |
| Decrypt with an RSA-2048 SSH key | 0.006 s | 0.034 s | 5.7 |
| Decrypt with an RSA-3072 SSH key | 0.007 s | 0.101 s | 14 |
| Decrypt with an Ed25519 SSH key with a passphrase (16 bcrypt rounds) | 0.16 s | 0.17 s | 1.1 |

`std/crypto/scrypt` takes 0.88 s for N = 2^18 and r = 8, and OpenSSL
takes 0.70 s. It keeps two 32-bit words in each `i64` slot, so its memory
is 256 MiB, as in age. The RSA decryption has no CRT and uses 26-bit
limbs, so it is slower than Go's. The decryption takes a fraction of the
time of a file of 64 KiB or more.
