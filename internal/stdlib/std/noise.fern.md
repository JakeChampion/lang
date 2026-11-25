# The Noise protocol framework

This module implements the Noise Protocol Framework, revision 34, for a secure channel between two peers. It has the patterns NN, NK, XX, IK and KK, and the modifiers `psk0`, `psk1` and `psk2`. The DH function is 25519, the cipher is ChaChaPoly or AESGCM, and the hash is SHA256.

## Model

A `Config` names the pattern, the cipher, the prologue, the keys of this side and the PSK. `initiator` and `responder` make a `Handshake` from a `Config`. The two sides then call `write_message` and `read_message` in turn, as the pattern tells. Each call consumes the `Handshake` and gives the next one with the result.

After the last message of the pattern, `split` makes a `Transport`. A `Transport` has two cipher states: one for the messages that this side sends, and one for the messages that it receives. `send` and `receive` also consume the `Transport` and give the next one. Thus a program cannot use one nonce for two messages.

`handshake_hash` gives the hash of the handshake. A program can use it to bind the channel to a higher protocol. `remote_static` gives the static public key of the peer after the handshake has it.

## Contracts

Each key is 32 bytes. A static key is the X25519 private key of this side. A remote static key is the X25519 public key of the peer. The pattern tells which keys a side must have before the handshake.

A key that the pattern does not use gives `UnusedKey`. A key that the pattern needs and does not have gives `MissingKey`. A PSK position other than 0, 1 or 2 gives `PskPosition`.

A message is at most 65535 bytes. A longer message gives `TooLong`, on the side that writes and on the side that reads. A message that is too short for its tokens gives `Short`. A message that does not authenticate gives `Decrypt`. An X25519 result of all zeros gives `Dh`.

At the nonce 2^64 - 1, a cipher state refuses to encrypt or decrypt with `NonceLimit`. Thus the nonce does not wrap. A refused call ends the handshake or the transport, because the call consumed it.

`initiator_derand` and `responder_derand` take the ephemeral private key from the caller. Use them only for tests. `initiator` and `responder` take the ephemeral key from the random source of the system.

The static keys, the ephemeral keys, the PSK, the chaining key and the cipher keys are secret. The drop of a `Handshake` or a `Transport` zeroes them.

## Executable example

```fern test name=std-noise-example
import "std/noise";

function main(): i32 {
  let s = __alloc_u8(32).with(0, 7 as u8);
  let Ok(rs) = noise.public_key(s) else { return 1; };
  let config = noise.Config { pattern: noise.NK, cipher: noise.ChaChaPoly, prologue: [], static_key: [], remote_static: rs, psk: noise.NoPsk };
  let Ok(i) = noise.initiator(config) else { return 2; };
  let Ok(r) = noise.responder(noise.Config { ...config, static_key: s, remote_static: [] }) else { return 3; };
  let Ok((m1, i)) = i.write_message("hello".as_bytes()) else { return 4; };
  let Ok((p1, r)) = r.read_message(m1) else { return 5; };
  let Ok((m2, r)) = r.write_message([]) else { return 6; };
  let Ok((_, i)) = i.read_message(m2) else { return 7; };
  let Ok(ti) = i.split() else { return 8; };
  let Ok(tr) = r.split() else { return 9; };
  let Ok((m3, _)) = ti.send("data".as_bytes()) else { return 10; };
  let Ok((p3, _)) = tr.receive(m3) else { return 11; };
  if (p1.len() != 5 || p3.len() != 4 || p3[0] != b'd') { return 12; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<the errors>>

<<the configuration>>

<<the cipher state>>

<<the symmetric state>>

<<the patterns>>

<<the handshake state>>

<<the transport>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements the Noise Protocol Framework (revision 34) with the
// patterns NN, NK, XX, IK and KK, the modifiers psk0 to psk2, 25519,
// ChaChaPoly or AESGCM, and SHA256.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/crypto";
import "std/crypto/aes_gcm";
import "std/crypto/chacha20poly1305";
import "std/crypto/x25519";
import "std/rand";
import "core/error";
import "core/cmp";
```

### `the errors`

```fern
<<the errors>>=
pub enum NoiseError {
  KeyLength(string, i32),
  MissingKey(string),
  UnusedKey(string),
  PskPosition(i32),
  Turn,
  Finished,
  NotFinished,
  Short(i32),
  TooLong(i32),
  Decrypt,
  Dh,
  NonceLimit,
}

impl error.Error for NoiseError {}

impl cmp.Display for NoiseError {}

pub function (e: NoiseError) to_string(): string {
  return e.message();
}

// One arm for each variant of the enum.
// fern-lint: allow cyclomatic-complexity
pub function (e: NoiseError) message(): string {
  return match (e) {
    KeyLength(what, n) => "noise: the " + what + " is " + n.to_string() + " bytes, want 32",
    MissingKey(what) => "noise: the pattern needs the " + what + " before the handshake",
    UnusedKey(what) => "noise: the pattern does not use the " + what + " before the handshake",
    PskPosition(n) => "noise: the PSK position is " + n.to_string() + ", want 0, 1 or 2",
    Turn => "noise: the other side writes the next message",
    Finished => "noise: the handshake has no more messages",
    NotFinished => "noise: the handshake has more messages",
    Short(n) => "noise: the message is " + n.to_string() + " bytes, too short for its tokens",
    TooLong(n) => "noise: the message is " + n.to_string() + " bytes, want at most 65535",
    Decrypt => "noise: the message does not authenticate",
    Dh => "noise: the X25519 shared secret is all zeros",
    NonceLimit => "noise: the cipher state has used its last nonce",
  };
}
```

### `the configuration`

`Psk(at, key)` adds the modifier `psk` followed by `at` to the pattern. `public_key` gives the X25519 public key of a static key, for the peer to pin.

```fern
<<the configuration>>=
pub const KEY_SIZE: i32 = 32;

pub const MAX_MESSAGE: i32 = 65535;

pub enum Pattern {
  NN,
  NK,
  XX,
  IK,
  KK,
}

pub enum Cipher {
  ChaChaPoly,
  AesGcm,
}

pub enum PskMode {
  NoPsk,
  Psk(i32, u8[]),
}

@secret
pub struct Config {
  pattern: Pattern,
  cipher: Cipher,
  prologue: u8[],
  static_key: u8[],
  remote_static: u8[],
  psk: PskMode,
}

pub function public_key(static_key: [u8]): Result[u8[], NoiseError] {
  if (static_key.len() != KEY_SIZE) {
    return Err(KeyLength("static key", static_key.len()));
  }
  let Ok(p) = x25519.public_key(static_key) else {
    return Err(KeyLength("static key", static_key.len()));
  };
  __ct_public(p);
  return Ok(p);
}

function __cat(a: [u8], b: [u8]): u8[] {
  return __bytes_into(__bytes_into(__alloc_u8(a.len() + b.len()), 0, a, 0, a.len()), a.len(), b, 0, b.len());
}

function __copy(a: [u8]): u8[] {
  return __bytes_into(__alloc_u8(a.len()), 0, a, 0, a.len());
}
```

### `the cipher state`

A cipher state holds a key, or no key, and the next nonce. Without a key, encryption gives the plaintext. The nonce of ChaChaPoly is four zero bytes and then the counter, little-endian. The nonce of AESGCM is four zero bytes and then the counter, big-endian. AESGCM uses a 256-bit key.

```fern
<<the cipher state>>=
const LAST_NONCE: u64 = 18446744073709551615u64;

enum AeadKey {
  NoKey,
  Chacha(chacha20poly1305.Key),
  Aes(aes_gcm.Key),
}

@secret
struct CipherState {
  key: AeadKey,
  n: u64,
}

function __empty_cipher(): CipherState {
  return CipherState { key: NoKey, n: 0 as u64 };
}

function __cipher_of(cipher: Cipher, k: [u8]): CipherState {
  let key = match (cipher) {
    ChaChaPoly => match (chacha20poly1305.key(k)) {
      Ok(c) => Chacha(c),
      Err(_) => NoKey,
    },
    AesGcm => match (aes_gcm.key(k)) {
      Ok(a) => Aes(a),
      Err(_) => NoKey,
    },
  };
  return CipherState { key: key, n: 0 as u64 };
}

function (cs: CipherState) has_key(): boolean {
  return match (cs.key) {
    NoKey => false,
    _ => true,
  };
}

function __nonce_le(n: u64): u8[] {
  let b = __alloc_u8(12);
  for i in 0..8 {
    b = b.with(4 + i, (n >> (8 * i) as u64 & 255 as u64) as u8);
  }
  return b;
}

function __nonce_be(n: u64): u8[] {
  let b = __alloc_u8(12);
  for i in 0..8 {
    b = b.with(11 - i, (n >> (8 * i) as u64 & 255 as u64) as u8);
  }
  return b;
}

function __encrypt(cs: CipherState, ad: [u8], plaintext: [u8]): Result[(u8[], CipherState), NoiseError] {
  if (cs.n == LAST_NONCE) {
    return Err(NonceLimit);
  }
  let sealed = match (cs.key) {
    NoKey => {
      return Ok((__copy(plaintext), cs));
    },
    Chacha(k) => k.seal(__nonce_le(cs.n), plaintext, ad),
    Aes(k) => k.seal(__nonce_be(cs.n), plaintext, ad),
  };
  let Ok(ct) = sealed else {
    return Err(TooLong(plaintext.len() + 16));
  };
  return Ok((ct, CipherState { ...cs, n: cs.n + 1 as u64 }));
}

function __decrypt(cs: CipherState, ad: [u8], ciphertext: [u8]): Result[(u8[], CipherState), NoiseError] {
  if (cs.n == LAST_NONCE) {
    return Err(NonceLimit);
  }
  let opened = match (cs.key) {
    NoKey => {
      return Ok((__copy(ciphertext), cs));
    },
    Chacha(k) => k.open(__nonce_le(cs.n), ciphertext, ad),
    Aes(k) => k.open(__nonce_be(cs.n), ciphertext, ad),
  };
  let Ok(pt) = opened else {
    return Err(Decrypt);
  };
  return Ok((pt, CipherState { ...cs, n: cs.n + 1 as u64 }));
}
```

### `the symmetric state`

`__hkdf` is the HKDF of the Noise specification: HKDF-SHA256 with the chaining key as the salt and no info. It gives two or three outputs of 32 bytes.

```fern
<<the symmetric state>>=
@secret
struct Symmetric {
  cipher: Cipher,
  ck: u8[],
  h: u8[],
  cs: CipherState,
}

function __hkdf(ck: [u8], ikm: [u8], outputs: i32): u8[] {
  let Ok(okm) = crypto.hkdf_sha256(ck, ikm, [], 32 * outputs) else {
    exit(134);
  };
  return okm;
}

function __initialize_symmetric(cipher: Cipher, name: string): Symmetric {
  let bytes = name.as_bytes();
  let h = if (bytes.len() <= 32) {
    __bytes_into(__alloc_u8(32), 0, bytes, 0, bytes.len())
  } else {
    crypto.sha256(bytes)
  };
  return Symmetric { cipher: cipher, ck: __copy(h), h: h, cs: __empty_cipher() };
}

function __mix_hash(s: Symmetric, data: [u8]): Symmetric {
  let h = crypto.sha256_new().update(s.h).update(data).finish();
  return Symmetric { ...s, h: h };
}

function __mix_key(s: Symmetric, ikm: [u8]): Symmetric {
  let okm = __hkdf(s.ck, ikm, 2);
  let next = Symmetric { ...s, ck: __copy(okm[0:32]), cs: __cipher_of(s.cipher, okm[32:64]) };
  crypto.erase(okm);
  return next;
}

function __mix_key_and_hash(s: Symmetric, ikm: [u8]): Symmetric {
  let okm = __hkdf(s.ck, ikm, 3);
  let next = __mix_hash(Symmetric { ...s, ck: __copy(okm[0:32]) }, okm[32:64]);
  next = Symmetric { ...next, cs: __cipher_of(next.cipher, okm[64:96]) };
  crypto.erase(okm);
  return next;
}

function __encrypt_and_hash(s: Symmetric, plaintext: [u8]): Result[(u8[], Symmetric), NoiseError] {
  let h = s.h;
  let (ct, cs) = __encrypt(s.cs, h, plaintext)?;
  return Ok((ct, __mix_hash(Symmetric { ...s, cs: cs }, ct)));
}

function __decrypt_and_hash(s: Symmetric, ciphertext: [u8]): Result[(u8[], Symmetric), NoiseError] {
  let h = s.h;
  let (pt, cs) = __decrypt(s.cs, h, ciphertext)?;
  return Ok((pt, __mix_hash(Symmetric { ...s, cs: cs }, ciphertext)));
}
```

### `the patterns`

A pattern is a flat list of tokens. `SEP` ends each message. The modifier `psk0` puts the token `psk` at the start of the first message. The modifier `psk1` or `psk2` puts it at the end of that message.

```fern
<<the patterns>>=
const SEP: i32 = 0;

const T_E: i32 = 1;

const T_S: i32 = 2;

const T_EE: i32 = 3;

const T_ES: i32 = 4;

const T_SE: i32 = 5;

const T_SS: i32 = 6;

const T_PSK: i32 = 7;

function __tokens(p: Pattern): i32[] {
  return match (p) {
    NN => [T_E, SEP, T_E, T_EE],
    NK => [T_E, T_ES, SEP, T_E, T_EE],
    XX => [T_E, SEP, T_E, T_EE, T_S, T_ES, SEP, T_S, T_SE],
    IK => [T_E, T_ES, T_S, T_SS, SEP, T_E, T_EE, T_SE],
    KK => [T_E, T_ES, T_SS, SEP, T_E, T_EE, T_SE],
  };
}

function __pattern_name(p: Pattern): string {
  return match (p) {
    NN => "NN",
    NK => "NK",
    XX => "XX",
    IK => "IK",
    KK => "KK",
  };
}

function __with_psk(tokens: [i32], at: i32): i32[] {
  let out: i32[] = [];
  if (at == 0) {
    out = out.append(T_PSK);
  }
  let message = 1;
  for i in 0..tokens.len() {
    if (tokens[i] == SEP) {
      if (message == at) {
        out = out.append(T_PSK);
      }
      message += 1;
    }
    out = out.append(tokens[i]);
  }
  if (message == at) {
    out = out.append(T_PSK);
  }
  return out;
}

// The pattern needs the static key of this side before the handshake when
// this side sends it in the pre-message, or when a token uses it.
function __needs_static(p: Pattern, initiator: boolean): boolean {
  return match (p) {
    NN => false,
    NK => !initiator,
    _ => true,
  };
}

function __needs_remote(p: Pattern, initiator: boolean): boolean {
  return match (p) {
    NK => initiator,
    IK => initiator,
    KK => true,
    _ => false,
  };
}
```

### `the handshake state`

`at` is the index of the first token of the next message. `msg` counts the messages so far. The initiator writes the messages with an even count. An `e` token also mixes the ephemeral key into the chaining key when the pattern has a PSK.

```fern
<<the handshake state>>=
@secret
pub opaque struct Handshake {
  sym: Symmetric,
  initiator: boolean,
  s: u8[],
  s_pub: u8[],
  e: u8[],
  e_pub: u8[],
  rs: u8[],
  re: u8[],
  psk: u8[],
  has_psk: boolean,
  tokens: i32[],
  at: i32,
  msg: i32,
  messages: i32,
}

pub function initiator(config: Config): Result[Handshake, NoiseError] {
  return __new(config, true, rand.bytes(32));
}

pub function responder(config: Config): Result[Handshake, NoiseError] {
  return __new(config, false, rand.bytes(32));
}

pub function initiator_derand(config: Config, ephemeral: [u8]): Result[Handshake, NoiseError] {
  return __new(config, true, ephemeral);
}

pub function responder_derand(config: Config, ephemeral: [u8]): Result[Handshake, NoiseError] {
  return __new(config, false, ephemeral);
}

function __check_key(what: string, key: [u8], needed: boolean): Result[void, NoiseError] {
  if (!needed) {
    if (key.len() != 0) {
      return Err(UnusedKey(what));
    }
    return Ok(());
  }
  if (key.len() == 0) {
    return Err(MissingKey(what));
  }
  if (key.len() != KEY_SIZE) {
    return Err(KeyLength(what, key.len()));
  }
  return Ok(());
}

// The tokens, the name suffix and the key of the PSK modifier.
function __psk_setup(tokens: i32[], mode: PskMode): Result[(i32[], string, u8[]), NoiseError] {
  return match (mode) {
    NoPsk => Ok((tokens, "", [])),
    Psk(at, key) => {
      if (at < 0 || at > 2) {
        return Err(PskPosition(at));
      }
      if (key.len() != KEY_SIZE) {
        return Err(KeyLength("PSK", key.len()));
      }
      Ok((__with_psk(tokens, at), "psk" + at.to_string(), __copy(key)))
    },
  };
}

function __cipher_name(c: Cipher): string {
  return match (c) {
    ChaChaPoly => "ChaChaPoly",
    AesGcm => "AESGCM",
  };
}

function __count_messages(tokens: [i32]): i32 {
  let messages = 1;
  for i in 0..tokens.len() {
    if (tokens[i] == SEP) {
      messages += 1;
    }
  }
  return messages;
}

function __static_public(s: [u8]): Result[u8[], NoiseError] {
  if (s.len() == 0) {
    return Ok([]);
  }
  return public_key(s);
}

// The pre-messages: the static key of the initiator first, then that of the responder.
function __pre_messages(sym: Symmetric, p: Pattern, initiator: boolean, s_pub: [u8], rs: [u8]): Symmetric {
  let (first, second) = if (initiator) { (s_pub, rs) } else { (rs, s_pub) };
  return match (p) {
    NK => __mix_hash(sym, second),
    IK => __mix_hash(sym, second),
    KK => __mix_hash(__mix_hash(sym, first), second),
    _ => sym,
  };
}

function __new(config: Config, initiator: boolean, ephemeral: [u8]): Result[Handshake, NoiseError] {
  let p = config.pattern;
  __check_key("static key", config.static_key, __needs_static(p, initiator))?;
  __check_key("remote static key", config.remote_static, __needs_remote(p, initiator))?;
  if (ephemeral.len() != KEY_SIZE) {
    return Err(KeyLength("ephemeral key", ephemeral.len()));
  }
  let (tokens, modifier, psk) = __psk_setup(__tokens(p), config.psk)?;
  let name = "Noise_" + __pattern_name(p) + modifier + "_25519_" + __cipher_name(config.cipher) + "_SHA256";
  let s = __copy(config.static_key);
  let s_pub = __static_public(s)?;
  let e = __copy(ephemeral);
  let e_pub = public_key(e)?;
  let rs = __copy(config.remote_static);
  let sym = __mix_hash(__initialize_symmetric(config.cipher, name), config.prologue);
  sym = __pre_messages(sym, p, initiator, s_pub, rs);
  let messages = __count_messages(tokens);
  return Ok(Handshake { sym: sym, initiator: initiator, s: s, s_pub: s_pub, e: e, e_pub: e_pub, rs: rs, re: [], psk: psk, has_psk: psk.len() != 0, tokens: tokens, at: 0, msg: 0, messages: messages });
}

pub function (hs: Handshake) is_finished(): boolean {
  return hs.msg == hs.messages;
}

pub function (hs: Handshake) handshake_hash(): u8[] {
  return __copy(hs.sym.h);
}

// The static public key of the peer: the remote static key of the
// configuration, or the key that a message gave. Empty before either.
pub function (hs: Handshake) remote_static(): u8[] {
  return __copy(hs.rs);
}

function __dh(private: [u8], public: [u8]): Result[u8[], NoiseError] {
  let Ok(shared) = x25519.x25519(private, public) else {
    return Err(Dh);
  };
  return Ok(shared);
}

// The keys of one DH token, by its letters: `e` or `s` of the initiator first.
function __dh_token(hs: Handshake, token: i32): Result[u8[], NoiseError] {
  let i = hs.initiator;
  if (token == T_EE) {
    return __dh(hs.e, hs.re);
  }
  if (token == T_SS) {
    return __dh(hs.s, hs.rs);
  }
  if (token == T_ES) {
    return if (i) { __dh(hs.e, hs.rs) } else { __dh(hs.s, hs.re) };
  }
  return if (i) { __dh(hs.s, hs.re) } else { __dh(hs.e, hs.rs) };
}

function __end(hs: Handshake): i32 {
  let j = hs.at;
  while (j < hs.tokens.len() && hs.tokens[j] != SEP) {
    j += 1;
  }
  return j;
}

function __check_turn(hs: Handshake, writing: boolean): Result[void, NoiseError] {
  if (hs.msg == hs.messages) {
    return Err(Finished);
  }
  let initiator_turn = hs.msg % 2 == 0;
  let my_turn = initiator_turn == hs.initiator;
  if (my_turn != writing) {
    return Err(Turn);
  }
  return Ok(());
}

// An `e` token mixes the ephemeral public key into the hash, and also into
// the chaining key when the pattern has a PSK.
function __mix_e(hs: Handshake, e_pub: [u8]): Symmetric {
  let sym = __mix_hash(hs.sym, e_pub);
  if (hs.has_psk) {
    return __mix_key(sym, e_pub);
  }
  return sym;
}

// A `psk` token or a DH token, the same on the two sides.
function __mix_token(hs: Handshake, t: i32): Result[Symmetric, NoiseError] {
  if (t == T_PSK) {
    return Ok(__mix_key_and_hash(hs.sym, hs.psk));
  }
  let shared = __dh_token(hs, t)?;
  let sym = __mix_key(hs.sym, shared);
  crypto.erase(shared);
  return Ok(sym);
}

pub function (own hs: Handshake) write_message(payload: [u8]): Result[(u8[], Handshake), NoiseError] {
  __check_turn(hs, true)?;
  let end = __end(hs);
  let out: u8[] = [];
  for j in hs.at..end {
    let t = hs.tokens[j];
    if (t == T_E) {
      out = __cat(out, hs.e_pub);
      hs = Handshake { ...hs, sym: __mix_e(hs, hs.e_pub) };
    } else if (t == T_S) {
      let (ct, sym) = __encrypt_and_hash(hs.sym, hs.s_pub)?;
      out = __cat(out, ct);
      hs = Handshake { ...hs, sym: sym };
    } else {
      hs = Handshake { ...hs, sym: __mix_token(hs, t)? };
    }
  }
  let (ct, sym) = __encrypt_and_hash(hs.sym, payload)?;
  out = __cat(out, ct);
  if (out.len() > MAX_MESSAGE) {
    return Err(TooLong(out.len()));
  }
  return Ok((out, Handshake { ...hs, sym: sym, at: end + 1, msg: hs.msg + 1 }));
}

// The n bytes of the message at pos.
function __take(message: [u8], pos: i32, n: i32): Result[[u8], NoiseError] {
  if (message.len() - pos < n) {
    return Err(Short(message.len()));
  }
  return Ok(message[pos:pos + n]);
}

function __tag_size(hs: Handshake): i32 {
  return if (hs.sym.cs.has_key()) { 16 } else { 0 };
}

// One token of a message at pos. It gives the position after the token.
function __read_token(own hs: Handshake, t: i32, message: [u8], pos: i32): Result[(i32, Handshake), NoiseError] {
  if (t == T_E) {
    let re = __copy(__take(message, pos, KEY_SIZE)?);
    return Ok((pos + KEY_SIZE, Handshake { ...hs, sym: __mix_e(hs, re), re: re }));
  }
  if (t == T_S) {
    let n = KEY_SIZE + __tag_size(hs);
    let (rs, sym) = __decrypt_and_hash(hs.sym, __take(message, pos, n)?)?;
    __ct_public(rs);
    return Ok((pos + n, Handshake { ...hs, sym: sym, rs: rs }));
  }
  return Ok((pos, Handshake { ...hs, sym: __mix_token(hs, t)? }));
}

pub function (own hs: Handshake) read_message(message: [u8]): Result[(u8[], Handshake), NoiseError] {
  __check_turn(hs, false)?;
  if (message.len() > MAX_MESSAGE) {
    return Err(TooLong(message.len()));
  }
  let end = __end(hs);
  let pos = 0;
  for j in hs.at..end {
    let t = hs.tokens[j];
    let (next_pos, next) = __read_token(hs, t, message, pos)?;
    pos = next_pos;
    hs = next;
  }
  __take(message, pos, __tag_size(hs))?;
  let (payload, sym) = __decrypt_and_hash(hs.sym, message[pos:message.len()])?;
  return Ok((payload, Handshake { ...hs, sym: sym, at: end + 1, msg: hs.msg + 1 }));
}
```

### `the transport`

`split` derives the two cipher keys from the chaining key. The first key encrypts the messages of the initiator, and the second key those of the responder.

A cipher state refuses the nonce 2^64 - 1 on the two sides.

```fern test name=std-noise-nonce-limit
<<*>>

function main(): i32 {
  let near = CipherState { ...__cipher_of(ChaChaPoly, __alloc_u8(32)), n: LAST_NONCE - 1 as u64 };
  let Ok((_, last)) = Transport { send: near, recv: __empty_cipher(), hash: [] }.send([1u8]) else { return 1; };
  let Err(NonceLimit()) = last.send([1u8]) else { return 2; };
  let at_limit = CipherState { ...__cipher_of(AesGcm, __alloc_u8(32)), n: LAST_NONCE };
  let Err(NonceLimit()) = Transport { send: __empty_cipher(), recv: at_limit, hash: [] }.receive(__alloc_u8(16)) else { return 3; };
  return 0;
}
```

```fern
<<the transport>>=
@secret
pub opaque struct Transport {
  send: CipherState,
  recv: CipherState,
  hash: u8[],
}

pub function (own hs: Handshake) split(): Result[Transport, NoiseError] {
  if (hs.msg != hs.messages) {
    return Err(NotFinished);
  }
  let okm = __hkdf(hs.sym.ck, [], 2);
  let c1 = __cipher_of(hs.sym.cipher, okm[0:32]);
  let c2 = __cipher_of(hs.sym.cipher, okm[32:64]);
  crypto.erase(okm);
  let hash = __copy(hs.sym.h);
  if (hs.initiator) {
    return Ok(Transport { send: c1, recv: c2, hash: hash });
  }
  return Ok(Transport { send: c2, recv: c1, hash: hash });
}

pub function (t: Transport) handshake_hash(): u8[] {
  return __copy(t.hash);
}

pub function (own t: Transport) send(payload: [u8]): Result[(u8[], Transport), NoiseError] {
  if (payload.len() > MAX_MESSAGE - 16) {
    return Err(TooLong(payload.len() + 16));
  }
  let (ct, cs) = __encrypt(t.send, [], payload)?;
  return Ok((ct, Transport { ...t, send: cs }));
}

pub function (own t: Transport) receive(message: [u8]): Result[(u8[], Transport), NoiseError] {
  if (message.len() > MAX_MESSAGE) {
    return Err(TooLong(message.len()));
  }
  let (pt, cs) = __decrypt(t.recv, [], message)?;
  return Ok((pt, Transport { ...t, recv: cs }));
}
```
