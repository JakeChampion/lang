# The Noise protocol framework

This module implements the Noise Protocol Framework, revision 34, for a secure channel between two peers. It supports the handshake patterns NN, NK, XX, IK and KK, and the modifiers `psk0`, `psk1` and `psk2`. The DH function is 25519, the cipher is ChaChaPoly or AESGCM, and the hash is SHA256.

## Model

A protocol name selects the pattern, the modifiers and the cipher, for example `Noise_XX_25519_ChaChaPoly_SHA256` or `Noise_IKpsk2_25519_AESGCM_SHA256`. Write two modifiers with a `+`, as in `NNpsk0+psk2`.

`setup` takes the name and the role of the peer, and gives a `Setup`. The `with_` methods of `Setup` add the prologue, the static keys, each PSK, and a fixed ephemeral key. `start` checks the keys against the pattern and gives a `Handshake`.

A `Handshake` writes and reads the messages of the pattern in turn. `write_message` and `read_message` consume the handshake and give the message or the payload and the next handshake. When `finished` is true, `split` gives a `Transport`.

A `Transport` holds two `CipherState` values, one to send and one to receive. Each `write_message` and `read_message` consumes the transport and gives the next one, as `hpke.Context` does. Thus a program cannot use a nonce twice.

The ephemeral key is 32 random bytes from the system unless the setup gives one. Give a fixed ephemeral key only to a test.

## Contracts

- Each key and each PSK is 32 bytes. A key of another length gives `KeyLength`.
- `start` gives `MissingKey` when the pattern needs a key that the setup does not have. It gives `UnusedKey` when the setup has a key that the pattern does not use. The count of PSKs must be the count of `psk` modifiers, or `start` gives `PskCount`.
- A handshake message or a transport message is at most 65535 bytes. A longer message gives `MessageLength`, on the side that writes it and on the side that reads it.
- A nonce is 64 bits, and the value 2^64 - 1 is reserved. A cipher state that comes to it gives `NonceLimit`.
- A message that does not authenticate gives `Decrypt`. A message that is shorter than its tokens gives `Short`. A DH result of all zeros gives `LowOrder`.
- A call out of turn gives `Turn`. This is a write or a read in the turn of the other peer, or a write or a read after the last handshake message. `split` before the last message gives `Unfinished`.
- An error consumes the handshake or the transport. Start a new handshake after an error.

## Executable example

```fern test name=std-noise-example
import "std/noise";

function main(): i32 {
  let name = "Noise_NN_25519_ChaChaPoly_SHA256";
  let Ok(a) = noise.setup(name, true) else { return 1; };
  let Ok(b) = noise.setup(name, false) else { return 2; };
  let Ok(i) = a.start() else { return 3; };
  let Ok(r) = b.start() else { return 4; };
  let Ok((m1, i)) = i.write_message([]) else { return 5; };
  let Ok((_, r)) = r.read_message(m1) else { return 6; };
  let Ok((m2, r)) = r.write_message([]) else { return 7; };
  let Ok((_, i)) = i.read_message(m2) else { return 8; };
  let Ok(ti) = i.split() else { return 9; };
  let Ok(tr) = r.split() else { return 10; };
  let Ok((sealed, _)) = ti.write_message("hello".as_bytes()) else { return 11; };
  let Ok((opened, _)) = tr.read_message(sealed) else { return 12; };
  if (opened.len() != 5 || opened[0] != b'h') { return 13; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<errors>>

<<protocol names>>

<<setup>>

<<symmetric state>>

<<handshake>>

<<cipher state>>

<<transport>>

<<bytes>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements the Noise Protocol Framework (revision 34) with
// DH 25519, ChaChaPoly or AESGCM, and SHA256.
import "std/crypto";
import "std/crypto/aes_gcm";
import "std/crypto/chacha20poly1305";
import "std/crypto/x25519";
import "std/rand";
import "std/string";
import "core/error";
import "core/cmp";
```

### `errors`

A `KeyLength` error names the key and keeps its length. A `PskCount` error keeps the count that the setup has and the count that the pattern needs.

```fern
<<errors>>=
pub enum NoiseError {
  Name(string),
  KeyLength(string, i32),
  MissingKey(string),
  UnusedKey(string),
  PskCount(i32, i32),
  Turn,
  Unfinished,
  MessageLength(i32),
  Short,
  Decrypt,
  LowOrder,
  NonceLimit,
}

impl error.Error for NoiseError {}

impl cmp.Display for NoiseError {}

pub function (e: NoiseError) to_string(): string {
  return e.message();
}

// fern-lint: allow cyclomatic-complexity
pub function (e: NoiseError) message(): string {
  return match (e) {
    Name(name) => "noise: the protocol name " + name + " is not supported",
    KeyLength(what, n) => "noise: the " + what + " is " + n.to_string() + " bytes, want 32",
    MissingKey(what) => "noise: the pattern needs the " + what,
    UnusedKey(what) => "noise: the pattern does not use the " + what,
    PskCount(n, want) => "noise: the setup has " + n.to_string() + " PSKs, the pattern needs " + want.to_string(),
    Turn => "noise: the message is out of turn",
    Unfinished => "noise: the handshake is not finished",
    MessageLength(n) => "noise: the message is " + n.to_string() + " bytes, the maximum is 65535",
    Short => "noise: the message is too short",
    Decrypt => "noise: the message does not authenticate",
    LowOrder => "noise: the DH result is all zeros",
    NonceLimit => "noise: the cipher state has used its last nonce",
  };
}
```

### `protocol names`

A pattern is a list of tokens. `END` closes each message. A `psk0` modifier puts a `psk` token first in the first message. A `pskN` modifier puts one last in message N.

```fern
<<protocol names>>=
const E: i32 = 0;

const S: i32 = 1;

const EE: i32 = 2;

const ES: i32 = 3;

const SE: i32 = 4;

const SS: i32 = 5;

const PSK: i32 = 6;

const END: i32 = 7;

const MAX_MESSAGE: i32 = 65535;

// The tokens of a pattern, and whether the initiator and the responder
// each have a pre-message `s`.
struct Pattern { tokens: i32[], pre_i: boolean, pre_r: boolean }

function __pattern(name: string): Option[Pattern] {
  return match (name) {
    "NN" => Some(Pattern { tokens: [E, END, E, EE, END], pre_i: false, pre_r: false }),
    "NK" => Some(Pattern { tokens: [E, ES, END, E, EE, END], pre_i: false, pre_r: true }),
    "XX" => Some(Pattern { tokens: [E, END, E, EE, S, ES, END, S, SE, END], pre_i: false, pre_r: false }),
    "IK" => Some(Pattern { tokens: [E, ES, S, SS, END, E, EE, SE, END], pre_i: false, pre_r: true }),
    "KK" => Some(Pattern { tokens: [E, ES, SS, END, E, EE, SE, END], pre_i: true, pre_r: true }),
    _ => None,
  };
}

// Puts a psk token first in the tokens when `at` is 0, else last in message `at`.
function __insert_psk(tokens: i32[], at: i32): Option[i32[]] {
  let out: i32[] = [];
  if (at == 0) {
    out = out.append(PSK);
  }
  let ends = 0;
  let done = at == 0;
  for t in tokens {
    if (t == END) {
      ends += 1;
      if (ends == at) {
        out = out.append(PSK);
        done = true;
      }
    }
    out = out.append(t);
  }
  if (!done) {
    return None;
  }
  return Some(out);
}

// The tokens of a pattern with the modifiers that follow its name, such as `psk0+psk2`.
function __with_modifiers(tokens: i32[], mods: string): Option[i32[]] {
  if (mods.len() == 0) {
    return Some(tokens);
  }
  let out = tokens;
  for m in mods.split("+") {
    if (m.len() != 4 || !m.starts_with("psk") || !__is_digit(m[3])) {
      return None;
    }
    out = __insert_psk(out, (m[3] - b'0') as i32)?;
  }
  return Some(out);
}

function __is_digit(c: u8): boolean {
  return c >= b'0' && c <= b'9';
}

// Whether the parts of a name other than the pattern are the supported ones.
function __supported(parts: string[]): boolean {
  if (parts.len() != 5) {
    return false;
  }
  let cipher = parts[3] == "AESGCM" || parts[3] == "ChaChaPoly";
  return parts[0] == "Noise" && parts[2] == "25519" && cipher && parts[4] == "SHA256";
}

// The pattern of a name, with its modifiers, and whether the cipher is AESGCM.
function __protocol(name: string): Result[(Pattern, boolean), NoiseError] {
  let parts = name.split("_");
  if (!__supported(parts)) {
    return Err(Name(name));
  }
  let Some(p) = __pattern(parts[1].take(2)) else {
    return Err(Name(name));
  };
  let Some(tokens) = __with_modifiers(p.tokens, parts[1].drop(2)) else {
    return Err(Name(name));
  };
  return Ok((Pattern { ...p, tokens: tokens }, parts[3] == "AESGCM"));
}
```

### `setup`

`start` finds which keys the pattern needs. A peer needs its own static key when it has a pre-message `s` or writes an `s` token. It needs the remote static key in advance when the peer has a pre-message `s`.

```fern
<<setup>>=
@secret
pub opaque struct Setup {
  name: string,
  aes: boolean,
  initiator: boolean,
  pattern: Pattern,
  prologue: u8[],
  s: u8[],
  rs: u8[],
  psks: u8[],
  e: u8[],
}

pub function setup(name: string, initiator: boolean): Result[Setup, NoiseError] {
  let (pattern, aes) = __protocol(name)?;
  return Ok(Setup { name: name, aes: aes, initiator: initiator, pattern: pattern, prologue: [], s: [], rs: [], psks: [], e: [] });
}

pub function (own c: Setup) with_prologue(prologue: [u8]): Setup {
  return Setup { ...c, prologue: __copy(prologue) };
}

pub function (own c: Setup) with_static_key(private_key: [u8]): Result[Setup, NoiseError] {
  if (private_key.len() != 32) {
    return Err(KeyLength("static private key", private_key.len()));
  }
  return Ok(Setup { ...c, s: __copy(private_key) });
}

pub function (own c: Setup) with_remote_static(public_key: [u8]): Result[Setup, NoiseError] {
  if (public_key.len() != 32) {
    return Err(KeyLength("remote static public key", public_key.len()));
  }
  return Ok(Setup { ...c, rs: __copy(public_key) });
}

// Adds the next PSK. The PSKs go to the psk tokens in the order of the tokens.
pub function (own c: Setup) with_psk(psk: [u8]): Result[Setup, NoiseError] {
  if (psk.len() != 32) {
    return Err(KeyLength("PSK", psk.len()));
  }
  return Ok(Setup { ...c, psks: __cat(c.psks, psk) });
}

// Sets the ephemeral private key in place of random bytes. Only a test does this.
pub function (own c: Setup) with_ephemeral(private_key: [u8]): Result[Setup, NoiseError] {
  if (private_key.len() != 32) {
    return Err(KeyLength("ephemeral private key", private_key.len()));
  }
  return Ok(Setup { ...c, e: __copy(private_key) });
}

// Whether the initiator writes message `msg`.
function __writer(msg: i32): boolean {
  return msg % 2 == 0;
}

// Whether this peer writes an `s` token.
function __writes_s(tokens: [i32], initiator: boolean): boolean {
  let msg = 0;
  for t in tokens {
    if (t == END) {
      msg += 1;
    } else if (t == S && __writer(msg) == initiator) {
      return true;
    }
  }
  return false;
}

// Refuses a key that the pattern needs and the setup does not have, or the reverse.
function __key_use(needed: boolean, key: [u8], what: string): Result[void, NoiseError] {
  if (needed && key.len() == 0) {
    return Err(MissingKey(what));
  }
  if (!needed && key.len() > 0) {
    return Err(UnusedKey(what));
  }
  return Ok(());
}

function __count(tokens: [i32], token: i32): i32 {
  let n = 0;
  for t in tokens {
    if (t == token) {
      n += 1;
    }
  }
  return n;
}

// The public key of a static private key, or an empty array for no key.
function __public(private_key: [u8]): Result[u8[], NoiseError] {
  if (private_key.len() == 0) {
    return Ok([]);
  }
  let Ok(k) = x25519.public_key(private_key) else {
    return Err(KeyLength("static private key", private_key.len()));
  };
  return Ok(k);
}

// The first chaining key and hash: the name, padded to 32 bytes or hashed.
function __initial_hash(name: [u8]): u8[] {
  if (name.len() <= 32) {
    return __bytes_into(__alloc_u8(32), 0, name, 0, name.len());
  }
  return crypto.sha256(name);
}

pub function (own c: Setup) start(): Result[Handshake, NoiseError] {
  let p = c.pattern;
  let own_pre = if (c.initiator) { p.pre_i } else { p.pre_r };
  let remote_pre = if (c.initiator) { p.pre_r } else { p.pre_i };
  __key_use(own_pre || __writes_s(p.tokens, c.initiator), c.s, "static private key")?;
  __key_use(remote_pre, c.rs, "remote static public key")?;
  let want = __count(p.tokens, PSK);
  if (c.psks.len() / 32 != want) {
    return Err(PskCount(c.psks.len() / 32, want));
  }
  let s_pub = __public(c.s)?;
  let h = __initial_hash(c.name.as_bytes());
  let hs = Handshake {
    aes: c.aes,
    initiator: c.initiator,
    tokens: p.tokens,
    at: 0,
    msg: 0,
    psk_mode: want > 0,
    ck: h,
    h: h,
    k: [],
    n: 0 as u64,
    s: c.s,
    s_pub: s_pub,
    e: c.e,
    e_pub: [],
    rs: c.rs,
    re: [],
    psks: c.psks,
    psk_at: 0,
  };
  return Ok(__mix_pre_messages(__mix_hash(hs, c.prologue), p, s_pub, c.rs));
}

// Hashes the static keys of the pre-messages: the key of the initiator first.
function __mix_pre_messages(own hs: Handshake, p: Pattern, s_pub: [u8], rs: [u8]): Handshake {
  let first = if (hs.initiator) { s_pub } else { rs };
  let second = if (hs.initiator) { rs } else { s_pub };
  if (p.pre_i) {
    hs = __mix_hash(hs, first);
  }
  if (p.pre_r) {
    hs = __mix_hash(hs, second);
  }
  return hs;
}
```

### `symmetric state`

These are the functions of the SymmetricState object of the specification. HKDF is the HKDF of section 4.3, with HMAC-SHA256. An empty `k` means that the handshake has no key yet. The HKDF temporary key and each DH result are erased after use.

```fern
<<symmetric state>>=
function __hkdf(ck: [u8], ikm: [u8]): (u8[], u8[], u8[]) {
  let temp = crypto.hmac_sha256(ck, ikm);
  let o1 = crypto.hmac_sha256(temp, [1u8]);
  let o2 = crypto.hmac_sha256(temp, __cat(o1, [2u8]));
  let o3 = crypto.hmac_sha256(temp, __cat(o2, [3u8]));
  crypto.erase(temp);
  return (o1, o2, o3);
}

function __mix_hash(own hs: Handshake, data: [u8]): Handshake {
  return Handshake { ...hs, h: crypto.sha256(__cat(hs.h, data)) };
}

function __mix_key(own hs: Handshake, ikm: [u8]): Handshake {
  let (ck, k, _) = __hkdf(hs.ck, ikm);
  return Handshake { ...hs, ck: ck, k: k, n: 0 as u64 };
}

function __mix_key_and_hash(own hs: Handshake, ikm: [u8]): Handshake {
  let (ck, h, k) = __hkdf(hs.ck, ikm);
  hs = Handshake { ...hs, ck: ck, k: k, n: 0 as u64 };
  return __mix_hash(hs, h);
}

function __encrypt_and_hash(own hs: Handshake, plaintext: [u8]): Result[(u8[], Handshake), NoiseError] {
  let ct = __copy(plaintext);
  if (hs.k.len() > 0) {
    ct = __seal(hs.aes, hs.k, hs.n, hs.h, plaintext)?;
    hs = Handshake { ...hs, n: hs.n + 1 as u64 };
  }
  return Ok((ct, __mix_hash(hs, ct)));
}

function __decrypt_and_hash(own hs: Handshake, ciphertext: [u8]): Result[(u8[], Handshake), NoiseError] {
  let pt = __copy(ciphertext);
  if (hs.k.len() > 0) {
    pt = __open(hs.aes, hs.k, hs.n, hs.h, ciphertext)?;
    hs = Handshake { ...hs, n: hs.n + 1 as u64 };
  }
  return Ok((pt, __mix_hash(hs, ciphertext)));
}
```

### `handshake`

`__dh` gives the DH result of a token. The `es` token is DH(e, rs) on the initiator and DH(s, re) on the responder, and `se` is the reverse.

```fern
<<handshake>>=
@secret
pub opaque struct Handshake {
  aes: boolean,
  initiator: boolean,
  tokens: i32[],
  at: i32,
  msg: i32,
  psk_mode: boolean,
  ck: u8[],
  h: u8[],
  k: u8[],
  n: u64,
  s: u8[],
  s_pub: u8[],
  e: u8[],
  e_pub: u8[],
  rs: u8[],
  re: u8[],
  psks: u8[],
  psk_at: i32,
}

pub function (hs: Handshake) finished(): boolean {
  return hs.at == hs.tokens.len();
}

// The hash of the handshake so far. After the last message, it identifies the session.
pub function (hs: Handshake) handshake_hash(): u8[] {
  return hs.h;
}

// The static public key of the peer, or an empty array before the handshake gives it.
pub function (hs: Handshake) remote_static(): u8[] {
  return hs.rs;
}

function __dh(hs: Handshake, t: i32): Result[u8[], NoiseError] {
  let pair = if (t == EE) {
    (hs.e, hs.re)
  } else if (t == SS) {
    (hs.s, hs.rs)
  } else if (hs.initiator == (t == ES)) {
    (hs.e, hs.rs)
  } else {
    (hs.s, hs.re)
  };
  let Ok(out) = x25519.x25519(pair.0, pair.1) else {
    return Err(LowOrder);
  };
  return Ok(out);
}

// The tokens that every message has: the DH tokens and `psk`.
function __mix_token(own hs: Handshake, t: i32): Result[Handshake, NoiseError] {
  if (t == PSK) {
    let psk = hs.psks[hs.psk_at * 32:hs.psk_at * 32 + 32];
    hs = __mix_key_and_hash(hs, psk);
    return Ok(Handshake { ...hs, psk_at: hs.psk_at + 1 });
  }
  let shared = __dh(hs, t)?;
  hs = __mix_key(hs, shared);
  crypto.erase(shared);
  return Ok(hs);
}

function __my_turn(hs: Handshake): boolean {
  return __writer(hs.msg) == hs.initiator;
}

// An `e` token hashes the ephemeral public key, and in a PSK handshake also mixes it into the key.
function __mix_ephemeral(own hs: Handshake, e_pub: [u8]): Handshake {
  hs = __mix_hash(hs, e_pub);
  if (hs.psk_mode) {
    hs = __mix_key(hs, e_pub);
  }
  return hs;
}

// The bytes that a token adds to a message that this peer writes.
function __write_token(own hs: Handshake, t: i32): Result[(u8[], Handshake), NoiseError] {
  if (t == E) {
    let e = if (hs.e.len() == 32) { hs.e } else { rand.bytes(32) };
    let Ok(e_pub) = x25519.public_key(e) else {
      return Err(KeyLength("ephemeral private key", e.len()));
    };
    return Ok((e_pub, __mix_ephemeral(Handshake { ...hs, e: e, e_pub: e_pub }, e_pub)));
  }
  if (t == S) {
    let s_pub = hs.s_pub;
    return __encrypt_and_hash(hs, s_pub);
  }
  return Ok(([], __mix_token(hs, t)?));
}

// Reads the bytes of a token from `message` at `at`, and gives the index after them.
function __read_token(own hs: Handshake, t: i32, message: [u8], at: i32): Result[(i32, Handshake), NoiseError] {
  let n = if (t == E) { 32 } else if (t != S) { 0 } else if (hs.k.len() > 0) { 48 } else { 32 };
  if (message.len() - at < n) {
    return Err(Short);
  }
  if (t == E) {
    let re = __copy(message[at:at + n]);
    return Ok((at + n, __mix_ephemeral(Handshake { ...hs, re: re }, re)));
  }
  if (t == S) {
    let (rs, next) = __decrypt_and_hash(hs, message[at:at + n])?;
    return Ok((at + n, Handshake { ...next, rs: rs }));
  }
  return Ok((at, __mix_token(hs, t)?));
}

pub function (own hs: Handshake) write_message(payload: [u8]): Result[(u8[], Handshake), NoiseError] {
  if (hs.finished() || !__my_turn(hs)) {
    return Err(Turn);
  }
  let out: u8[] = [];
  let i = hs.at;
  while (hs.tokens[i] != END) {
    let t = hs.tokens[i];
    let (bytes, next) = __write_token(hs, t)?;
    out = __cat(out, bytes);
    hs = next;
    i += 1;
  }
  let (ct, next) = __encrypt_and_hash(hs, payload)?;
  out = __cat(out, ct);
  if (out.len() > MAX_MESSAGE) {
    return Err(MessageLength(out.len()));
  }
  return Ok((out, Handshake { ...next, at: i + 1, msg: next.msg + 1 }));
}

pub function (own hs: Handshake) read_message(message: [u8]): Result[(u8[], Handshake), NoiseError] {
  if (message.len() > MAX_MESSAGE) {
    return Err(MessageLength(message.len()));
  }
  if (hs.finished() || __my_turn(hs)) {
    return Err(Turn);
  }
  let at = 0;
  let i = hs.at;
  while (hs.tokens[i] != END) {
    let t = hs.tokens[i];
    let (next_at, next) = __read_token(hs, t, message, at)?;
    at = next_at;
    hs = next;
    i += 1;
  }
  if (hs.k.len() > 0 && message.len() - at < 16) {
    return Err(Short);
  }
  let (payload, next) = __decrypt_and_hash(hs, message[at:message.len()])?;
  return Ok((payload, Handshake { ...next, at: i + 1, msg: next.msg + 1 }));
}

// The transport of a finished handshake. The initiator sends with the first key of Split.
pub function (own hs: Handshake) split(): Result[Transport, NoiseError] {
  if (!hs.finished()) {
    return Err(Unfinished);
  }
  let (k1, k2, _) = __hkdf(hs.ck, []);
  let c1 = CipherState { aes: hs.aes, k: k1, n: 0 as u64 };
  let c2 = CipherState { aes: hs.aes, k: k2, n: 0 as u64 };
  if (hs.initiator) {
    return Ok(Transport { send: c1, recv: c2, h: hs.h });
  }
  return Ok(Transport { send: c2, recv: c1, h: hs.h });
}
```

### `cipher state`

The nonce of ChaChaPoly is 4 zero bytes and then the counter in little-endian order. The nonce of AESGCM is 4 zero bytes and then the counter in big-endian order. `rekey` is the default REKEY of section 4.2: the first 32 bytes of ENCRYPT(k, 2^64 - 1, empty, 32 zeros). Only `rekey` uses that nonce.

The expected keys of the test below come from two Go packages: `golang.org/x/crypto/chacha20poly1305` and `crypto/cipher`.

```fern test name=std-noise-nonce-limit
import "std/hex";
<<*>>

function main(): i32 {
  let k = __alloc_u8(32);
  let i = 0;
  while (i < 32) {
    k = k.with(i, i as u8);
    i += 1;
  }
  let c = CipherState { aes: false, k: k, n: LAST_NONCE - 1 as u64 };
  let Ok((_, last)) = c.encrypt_with_ad([], [1u8]) else { return 1; };
  let Err(NonceLimit()) = last.encrypt_with_ad([], [1u8]) else { return 2; };
  let r = CipherState { aes: true, k: k, n: LAST_NONCE };
  let Err(NonceLimit()) = r.decrypt_with_ad([], __alloc_u8(16)) else { return 3; };
  let a = CipherState { aes: false, k: k, n: LAST_NONCE }.rekey();
  if (hex.encode(a.k) != "50835543a205b22c9323f2022bc4f67d838f90e61d5ccf33c4513e01f85b5042" || a.n != LAST_NONCE) { return 4; }
  let g = CipherState { aes: true, k: k, n: 7 as u64 }.rekey();
  if (hex.encode(g.k) != "0201675c87335949b909793da5bb4d92fcf6d44b92a6e0792b6ae48b1881259d" || g.n != 7 as u64) { return 5; }
  return 0;
}
```

```fern
<<cipher state>>=
const LAST_NONCE: u64 = 18446744073709551615u64;

@secret
pub opaque struct CipherState { aes: boolean, k: u8[], n: u64 }

function __nonce(aes: boolean, n: u64): u8[] {
  let out = __alloc_u8(12);
  let v = n;
  let i = 0;
  while (i < 8) {
    let at = if (aes) { 11 - i } else { 4 + i };
    out = out.with(at, (v % (256 as u64)) as u8);
    v = v / (256 as u64);
    i += 1;
  }
  return out;
}

// The AEAD of the cipher, without the check of the reserved nonce.
function __aead_seal(aes: boolean, k: [u8], n: u64, ad: [u8], plaintext: [u8]): Result[u8[], NoiseError] {
  let nonce = __nonce(aes, n);
  if (aes) {
    let Ok(key) = aes_gcm.key(k) else {
      return Err(KeyLength("cipher key", k.len()));
    };
    let Ok(ct) = key.seal(nonce, plaintext, ad) else {
      return Err(MessageLength(plaintext.len() + 16));
    };
    return Ok(ct);
  }
  let Ok(key) = chacha20poly1305.key(k) else {
    return Err(KeyLength("cipher key", k.len()));
  };
  let Ok(ct) = key.seal(nonce, plaintext, ad) else {
    return Err(MessageLength(plaintext.len() + 16));
  };
  return Ok(ct);
}

function __seal(aes: boolean, k: [u8], n: u64, ad: [u8], plaintext: [u8]): Result[u8[], NoiseError] {
  if (n == LAST_NONCE) {
    return Err(NonceLimit);
  }
  return __aead_seal(aes, k, n, ad, plaintext);
}

function __open(aes: boolean, k: [u8], n: u64, ad: [u8], ciphertext: [u8]): Result[u8[], NoiseError] {
  if (n == LAST_NONCE) {
    return Err(NonceLimit);
  }
  let nonce = __nonce(aes, n);
  if (aes) {
    let Ok(key) = aes_gcm.key(k) else {
      return Err(Decrypt);
    };
    let Ok(pt) = key.open(nonce, ciphertext, ad) else {
      return Err(Decrypt);
    };
    return Ok(pt);
  }
  let Ok(key) = chacha20poly1305.key(k) else {
    return Err(Decrypt);
  };
  let Ok(pt) = key.open(nonce, ciphertext, ad) else {
    return Err(Decrypt);
  };
  return Ok(pt);
}

pub function (own c: CipherState) encrypt_with_ad(ad: [u8], plaintext: [u8]): Result[(u8[], CipherState), NoiseError] {
  if (plaintext.len() > MAX_MESSAGE - 16) {
    return Err(MessageLength(plaintext.len() + 16));
  }
  let ct = __seal(c.aes, c.k, c.n, ad, plaintext)?;
  return Ok((ct, CipherState { ...c, n: c.n + 1 as u64 }));
}

pub function (own c: CipherState) decrypt_with_ad(ad: [u8], ciphertext: [u8]): Result[(u8[], CipherState), NoiseError] {
  if (ciphertext.len() > MAX_MESSAGE) {
    return Err(MessageLength(ciphertext.len()));
  }
  let pt = __open(c.aes, c.k, c.n, ad, ciphertext)?;
  return Ok((pt, CipherState { ...c, n: c.n + 1 as u64 }));
}

// The count of messages that the cipher state encrypted or decrypted.
pub function (c: CipherState) nonce(): u64 {
  return c.n;
}

pub function (own c: CipherState) rekey(): CipherState {
  let Ok(ct) = __aead_seal(c.aes, c.k, LAST_NONCE, [], __alloc_u8(32)) else {
    return c;
  };
  return CipherState { ...c, k: __copy(ct[0:32]) };
}
```

### `transport`

```fern
<<transport>>=
@secret
pub opaque struct Transport { send: CipherState, recv: CipherState, h: u8[] }

pub function (own t: Transport) write_message(payload: [u8]): Result[(u8[], Transport), NoiseError] {
  let (ct, send) = t.send.encrypt_with_ad([], payload)?;
  return Ok((ct, Transport { ...t, send: send }));
}

pub function (own t: Transport) read_message(message: [u8]): Result[(u8[], Transport), NoiseError] {
  let (pt, recv) = t.recv.decrypt_with_ad([], message)?;
  return Ok((pt, Transport { ...t, recv: recv }));
}

pub function (t: Transport) handshake_hash(): u8[] {
  return t.h;
}

// The cipher states, to send and to receive.
pub function (own t: Transport) cipher_states(): (CipherState, CipherState) {
  return (t.send, t.recv);
}

pub function (own t: Transport) rekey_send(): Transport {
  return Transport { ...t, send: t.send.rekey() };
}

pub function (own t: Transport) rekey_recv(): Transport {
  return Transport { ...t, recv: t.recv.rekey() };
}
```

### `bytes`

```fern
<<bytes>>=
function __copy(a: [u8]): u8[] {
  return __bytes_into(__alloc_u8(a.len()), 0, a, 0, a.len());
}

function __cat(a: [u8], b: [u8]): u8[] {
  return __bytes_into(__bytes_into(__alloc_u8(a.len() + b.len()), 0, a, 0, a.len()), a.len(), b, 0, b.len());
}
```
