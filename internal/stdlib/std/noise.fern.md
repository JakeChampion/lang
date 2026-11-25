# The Noise Protocol Framework

This module implements the Noise Protocol Framework, revision 34. It supports the handshake patterns NN, NK, XX, IK and KK, and the PSK modifiers `psk0` to `psk9`. The DH function is 25519, the cipher is ChaChaPoly or AESGCM, and the hash is SHA256.

## Model

A `Protocol` is the pattern, the positions of its PSK tokens and the cipher. `parse` reads a protocol name such as `Noise_XXpsk3_25519_ChaChaPoly_SHA256`, and `(p: Protocol) name()` gives the name back.

A `Config` gives the role, the prologue, the local static key pair, the remote static public key of a pre-message and the PSKs. `handshake` makes a `Handshake` from a `Config`. The two peers then call `write_message` and `read_message` in turn, from the first message of the pattern to the last. After the last message, `split` makes a `Transport`. A `Transport` has one cipher state to send and one cipher state to receive.

Each of `write_message`, `read_message`, `write` and `read` consumes its state and gives the next state with the result. A program therefore cannot send two messages with one nonce. A refused message ends the state, and the peers must start a new handshake.

## Contracts

A key, a public key and a PSK are each 32 bytes. Each other length gives `KeyLength`, with the name of the value and its length. When the pattern needs a key that the `Config` does not give, `handshake` gives `MissingKey` with the name of the key. This key can be the local static key, the remote static key of a pre-message or a PSK. A remote static key for a pattern without a pre-message of that key gives `UnusedKey`, because the handshake does not check that key. Use `remote_static` after the handshake to read the key that the peer sent, and compare it with a key that you trust.

A message, and the payload of a transport message, is at most 65535 bytes. A longer message gives `MessageLength`. A message that does not authenticate gives `Decrypt`, and a message too short for its tokens gives `Truncated`.

A DH output of all zeros gives `LowOrder`. A cipher state refuses its nonce 2^64 - 1 with `NonceLimit`. `write_message` and `read_message` give `Order` when it is the turn of the peer, and `Finished` after the last message. `split` before the last message gives `Order`.

When the `Config` gives no `fixed_ephemeral`, the handshake takes a fresh ephemeral key from `std/rand` when it writes the `e` token. Give a `fixed_ephemeral` only to reproduce a test vector: an ephemeral key that is used two times breaks the security of the handshake.

The records that hold keys are `@secret`, so their drop zeroes the arrays that they hold alone. The PSKs are in an array of arrays, which the drop does not zero.

## Executable example

```fern test name=std-noise-example
import "std/noise";

function main(): i32 {
  let Ok(protocol) = noise.parse("Noise_XX_25519_ChaChaPoly_SHA256") else { return 1; };
  let Ok(alice_key) = noise.key_pair(__alloc_u8(32).with(0, 1 as u8)) else { return 2; };
  let Ok(bob_key) = noise.key_pair(__alloc_u8(32).with(0, 2 as u8)) else { return 3; };
  let Ok(alice) = noise.handshake(noise.initiator(protocol, alice_key)) else { return 4; };
  let Ok(bob) = noise.handshake(noise.responder(protocol, bob_key)) else { return 5; };
  let Ok((m1, alice)) = alice.write_message([]) else { return 6; };
  let Ok((_, bob)) = bob.read_message(m1) else { return 7; };
  let Ok((m2, bob)) = bob.write_message([]) else { return 8; };
  let Ok((_, alice)) = alice.read_message(m2) else { return 9; };
  let Ok((m3, alice)) = alice.write_message([]) else { return 10; };
  let Ok((_, bob)) = bob.read_message(m3) else { return 11; };
  let Ok(a) = alice.split() else { return 12; };
  let Ok(b) = bob.split() else { return 13; };
  let Ok((sealed, _)) = a.write("hello".as_bytes()) else { return 14; };
  let Ok((opened, _)) = b.read(sealed) else { return 15; };
  if (opened.len() != 5 || opened[0] != b'h') { return 16; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum NoiseError>>

<<4 the protocol>>

<<5 keys>>

<<6 the cipher state>>

<<7 the symmetric state>>

<<8 the handshake state>>

<<9 the transport state>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements the Noise Protocol Framework (revision 34) with
// the patterns NN, NK, XX, IK and KK, the PSK modifiers, X25519, ChaChaPoly,
// AESGCM and SHA256.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto";
import "std/crypto/aes_gcm";
import "std/crypto/chacha20poly1305";
import "std/crypto/x25519";
import "std/option";
import "std/rand";
import "std/string";
import "core/error";
import "core/cmp";
```

### `3 pub enum NoiseError`

```fern
<<3 pub enum NoiseError>>=
pub enum NoiseError {
  Name(string),
  KeyLength(string, i32),
  MissingKey(string),
  UnusedKey(string),
  LowOrder,
  Decrypt,
  Truncated,
  MessageLength(i32),
  NonceLimit,
  Order,
  Finished,
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
    LowOrder => "noise: the X25519 shared secret is all zeros",
    Decrypt => "noise: the message does not authenticate",
    Truncated => "noise: the message is too short",
    MessageLength(n) => "noise: the message is " + n.to_string() + " bytes, want at most 65535",
    NonceLimit => "noise: the cipher state has used its last nonce",
    Order => "noise: the message is not in the order of the pattern",
    Finished => "noise: the handshake has no more messages",
  };
}
```

### `4 the protocol`

The tokens of each pattern are those of section 7 of the specification. A pre-message can hold only an `s` token, so `__pre` gives a boolean for each side. The modifier `psk0` puts a `psk` token at the start of the first message. The modifier `pskN` puts one at the end of message N.

```fern
<<4 the protocol>>=
pub enum Pattern {
  NN,
  NK,
  XX,
  IK,
  KK,
}

pub enum Cipher {
  ChaChaPoly,
  AESGCM,
}

pub struct Protocol {
  pattern: Pattern,
  psks: i32[],
  cipher: Cipher,
}

enum Token {
  E,
  S,
  EE,
  ES,
  SE,
  SS,
  Psk,
}

pub const MAX_MESSAGE: i32 = 65535;

function __pattern_name(p: Pattern): string {
  return match (p) {
    NN => "NN",
    NK => "NK",
    XX => "XX",
    IK => "IK",
    KK => "KK",
  };
}

function __cipher_name(c: Cipher): string {
  return match (c) {
    ChaChaPoly => "ChaChaPoly",
    AESGCM => "AESGCM",
  };
}

pub function (p: Protocol) name(): string {
  let mods = "";
  for i in 0..p.psks.len() {
    if (i > 0) {
      mods += "+";
    }
    mods += "psk" + p.psks[i].to_string();
  }
  return "Noise_" + __pattern_name(p.pattern) + mods + "_25519_" + __cipher_name(p.cipher) + "_SHA256";
}

function __parse_pattern(s: string): Option[Pattern] {
  return match (s) {
    "NN" => Some(NN),
    "NK" => Some(NK),
    "XX" => Some(XX),
    "IK" => Some(IK),
    "KK" => Some(KK),
    _ => None,
  };
}

function __parse_cipher(s: string): Option[Cipher] {
  return match (s) {
    "ChaChaPoly" => Some(ChaChaPoly),
    "AESGCM" => Some(AESGCM),
    _ => None,
  };
}

// The modifiers after the pattern: "psk0+psk2" gives [0, 2].
function __parse_psks(s: string, messages: i32): Option[i32[]] {
  let out: i32[] = [];
  if (s == "") {
    return Some(out);
  }
  for m in s.split("+") {
    if (m.len() != 4 || !m.starts_with("psk")) {
      return None;
    }
    let digit = m.as_bytes()[3] as i32 - b'0' as i32;
    if (digit < 0 || digit > messages || out.len() > 0 && digit <= out[out.len() - 1]) {
      return None;
    }
    out = out.append(digit);
  }
  return Some(out);
}

pub function parse(name: string): Result[Protocol, NoiseError] {
  let parts = name.split("_");
  if (parts.len() != 5 || parts[0] != "Noise" || parts[2] != "25519" || parts[4] != "SHA256" || parts[1].len() < 2) {
    return Err(Name(name));
  }
  let Some(pattern) = __parse_pattern(parts[1].take(2)) else {
    return Err(Name(name));
  };
  let Some(psks) = __parse_psks(parts[1].drop(2), __messages(pattern).len()) else {
    return Err(Name(name));
  };
  let Some(cipher) = __parse_cipher(parts[3]) else {
    return Err(Name(name));
  };
  return Ok(Protocol { pattern: pattern, psks: psks, cipher: cipher });
}

// The pre-messages: if the initiator and the responder each send a static key.
function __pre(p: Pattern): (boolean, boolean) {
  return match (p) {
    NN => (false, false),
    NK => (false, true),
    XX => (false, false),
    IK => (false, true),
    KK => (true, true),
  };
}

function __messages(p: Pattern): Token[][] {
  return match (p) {
    NN => [[E], [E, EE]],
    NK => [[E, ES], [E, EE]],
    XX => [[E], [E, EE, S, ES], [S, SE]],
    IK => [[E, ES, S, SS], [E, EE, SE]],
    KK => [[E, ES, SS], [E, EE, SE]],
  };
}

function __has(xs: [i32], x: i32): boolean {
  for v in xs {
    if (v == x) {
      return true;
    }
  }
  return false;
}

function __with_psks(own messages: Token[][], psks: [i32]): Token[][] {
  let out: Token[][] = [];
  for i in 0..messages.len() {
    let tokens: Token[] = [];
    if (i == 0 && __has(psks, 0)) {
      tokens = tokens.append(Psk);
    }
    for t in messages[i] {
      tokens = tokens.append(t);
    }
    if (__has(psks, i + 1)) {
      tokens = tokens.append(Psk);
    }
    out = out.append(tokens);
  }
  return out;
}
```

### `5 keys`

```fern
<<5 keys>>=
pub const KEY_SIZE: i32 = 32;

@secret
pub struct KeyPair {
  private_key: u8[],
  public_key: u8[],
}

pub function key_pair(private_key: [u8]): Result[KeyPair, NoiseError] {
  if (private_key.len() != KEY_SIZE) {
    return Err(KeyLength("private key", private_key.len()));
  }
  let Ok(public_key) = x25519.public_key(private_key) else {
    return Err(KeyLength("private key", private_key.len()));
  };
  __ct_public(public_key);
  return Ok(KeyPair { private_key: __take(private_key, 0, KEY_SIZE), public_key: public_key });
}

pub function generate_key_pair(): KeyPair {
  let private_key = rand.bytes(KEY_SIZE);
  let Ok(k) = key_pair(private_key) else {
    exit(134);
  };
  crypto.erase(private_key);
  return k;
}

function __take(b: [u8], at: i32, n: i32): u8[] {
  return __bytes_into(__alloc_u8(n), 0, b, at, n);
}

function __dh(k: KeyPair, public_key: [u8]): Result[u8[], NoiseError] {
  let Ok(shared) = x25519.x25519(k.private_key, public_key) else {
    return Err(LowOrder);
  };
  return Ok(shared);
}
```

### `6 the cipher state`

A cipher state with an empty key passes each plaintext through, as section 5.1 of the specification says. ChaChaPoly takes the nonce as 32 zero bits and the counter in 64 bits, little-endian. AESGCM takes the counter big-endian.

```fern test name=std-noise-nonce-limit
<<*>>

function main(): i32 {
  let c = CipherState { cipher: ChaChaPoly, k: __alloc_u8(32), n: LAST_NONCE - 1 as u64 };
  let Ok((sealed, last)) = __encrypt_with_ad(c, [], [1u8]) else { return 1; };
  let Err(NonceLimit()) = __encrypt_with_ad(last, [], [1u8]) else { return 2; };
  let r = CipherState { cipher: AESGCM, k: __alloc_u8(32), n: LAST_NONCE };
  let Err(NonceLimit()) = __decrypt_with_ad(r, [], sealed) else { return 3; };
  if (__nonce(ChaChaPoly, 258 as u64)[4] != 2u8 || __nonce(AESGCM, 258 as u64)[10] != 1u8) { return 4; }
  return 0;
}
```

```fern
<<6 the cipher state>>=
@secret
struct CipherState {
  cipher: Cipher,
  k: u8[],
  n: u64,
}

const LAST_NONCE: u64 = 18446744073709551615u64;

function __nonce(cipher: Cipher, n: u64): u8[] {
  let b = __alloc_u8(12);
  for i in 0..8 {
    let byte = (n >> (8 * i) as u64 & 255 as u64) as u8;
    b = match (cipher) {
      ChaChaPoly => b.with(4 + i, byte),
      AESGCM => b.with(11 - i, byte),
    };
  }
  return b;
}

function __seal(cipher: Cipher, k: [u8], n: u64, ad: [u8], plaintext: [u8]): Result[u8[], NoiseError] {
  let nonce = __nonce(cipher, n);
  match (cipher) {
    ChaChaPoly => {
      let Ok(key) = chacha20poly1305.key(k) else {
        return Err(KeyLength("cipher key", k.len()));
      };
      let sealed = key.seal(nonce, plaintext, ad);
      key.erase();
      let Ok(out) = sealed else {
        return Err(MessageLength(plaintext.len()));
      };
      return Ok(out);
    },
    AESGCM => {
      let Ok(key) = aes_gcm.key(k) else {
        return Err(KeyLength("cipher key", k.len()));
      };
      let sealed = key.seal(nonce, plaintext, ad);
      key.erase();
      let Ok(out) = sealed else {
        return Err(MessageLength(plaintext.len()));
      };
      return Ok(out);
    }
  }
}

function __open(cipher: Cipher, k: [u8], n: u64, ad: [u8], sealed: [u8]): Result[u8[], NoiseError] {
  let nonce = __nonce(cipher, n);
  match (cipher) {
    ChaChaPoly => {
      let Ok(key) = chacha20poly1305.key(k) else {
        return Err(KeyLength("cipher key", k.len()));
      };
      let opened = key.open(nonce, sealed, ad);
      key.erase();
      let Ok(out) = opened else {
        return Err(Decrypt);
      };
      return Ok(out);
    },
    AESGCM => {
      let Ok(key) = aes_gcm.key(k) else {
        return Err(KeyLength("cipher key", k.len()));
      };
      let opened = key.open(nonce, sealed, ad);
      key.erase();
      let Ok(out) = opened else {
        return Err(Decrypt);
      };
      return Ok(out);
    }
  }
}

function __encrypt_with_ad(c: CipherState, ad: [u8], plaintext: [u8]): Result[(u8[], CipherState), NoiseError] {
  if (c.k.len() == 0) {
    return Ok((__take(plaintext, 0, plaintext.len()), c));
  }
  if (c.n == LAST_NONCE) {
    return Err(NonceLimit);
  }
  let sealed = __seal(c.cipher, c.k, c.n, ad, plaintext)?;
  return Ok((sealed, CipherState { cipher: c.cipher, k: c.k, n: c.n + 1 as u64 }));
}

function __decrypt_with_ad(c: CipherState, ad: [u8], sealed: [u8]): Result[(u8[], CipherState), NoiseError] {
  if (c.k.len() == 0) {
    return Ok((__take(sealed, 0, sealed.len()), c));
  }
  if (c.n == LAST_NONCE) {
    return Err(NonceLimit);
  }
  if (sealed.len() < 16) {
    return Err(Truncated);
  }
  let opened = __open(c.cipher, c.k, c.n, ad, sealed)?;
  return Ok((opened, CipherState { cipher: c.cipher, k: c.k, n: c.n + 1 as u64 }));
}
```

### `7 the symmetric state`

`__hkdf` is the HKDF function of section 4.3 of the specification, with HMAC-SHA256. It gives two or three outputs of 32 bytes, one after the other.

```fern
<<7 the symmetric state>>=
@secret
struct Symmetric {
  cs: CipherState,
  ck: u8[],
  h: u8[],
}

function __hkdf(ck: [u8], ikm: [u8], outputs: i32): u8[] {
  let temp = crypto.hmac_sha256(ck, ikm);
  let out1 = crypto.hmac_sha256(temp, [1u8]);
  let out2 = crypto.hmac_sha256(temp, out1.concat([2u8]));
  let out = out1.concat(out2);
  if (outputs == 3) {
    let out3 = crypto.hmac_sha256(temp, out2.concat([3u8]));
    out = out.concat(out3);
    crypto.erase(out3);
  }
  crypto.erase(temp);
  crypto.erase(out1);
  crypto.erase(out2);
  return out;
}

function __symmetric(protocol: Protocol): Symmetric {
  let name = protocol.name().as_bytes();
  let h = if (name.len() <= 32) {
    __bytes_into(__alloc_u8(32), 0, name, 0, name.len())
  } else {
    crypto.sha256(name)
  };
  let cs = CipherState { cipher: protocol.cipher, k: [], n: 0 as u64 };
  return Symmetric { cs: cs, ck: h, h: h };
}

function __mix_hash(s: Symmetric, data: [u8]): Symmetric {
  let h = crypto.sha256_new().update(s.h).update(data).finish();
  return Symmetric { cs: s.cs, ck: s.ck, h: h };
}

function __mix_key(s: Symmetric, ikm: [u8]): Symmetric {
  let out = __hkdf(s.ck, ikm, 2);
  let cs = CipherState { cipher: s.cs.cipher, k: __take(out, 32, 32), n: 0 as u64 };
  let ck = __take(out, 0, 32);
  crypto.erase(out);
  return Symmetric { cs: cs, ck: ck, h: s.h };
}

function __mix_key_and_hash(s: Symmetric, ikm: [u8]): Symmetric {
  let out = __hkdf(s.ck, ikm, 3);
  let cs = CipherState { cipher: s.cs.cipher, k: __take(out, 64, 32), n: 0 as u64 };
  let mixed = __mix_hash(Symmetric { cs: cs, ck: __take(out, 0, 32), h: s.h }, out[32:64]);
  crypto.erase(out);
  return mixed;
}

function __encrypt_and_hash(s: Symmetric, plaintext: [u8]): Result[(u8[], Symmetric), NoiseError] {
  let (sealed, cs) = __encrypt_with_ad(s.cs, s.h, plaintext)?;
  return Ok((sealed, __mix_hash(Symmetric { cs: cs, ck: s.ck, h: s.h }, sealed)));
}

function __decrypt_and_hash(s: Symmetric, sealed: [u8]): Result[(u8[], Symmetric), NoiseError] {
  let (opened, cs) = __decrypt_with_ad(s.cs, s.h, sealed)?;
  return Ok((opened, __mix_hash(Symmetric { cs: cs, ck: s.ck, h: s.h }, sealed)));
}
```

### `8 the handshake state`

`initiator` and `responder` give a `Config` with an empty prologue, no remote static key and no PSK. `handshake` checks the keys against the pattern, mixes the protocol name, the prologue and the pre-messages, and gives the state before the first message. The initiator writes the messages with an even index, and the responder writes the others.

The `psk` modifier also mixes each ephemeral public key as a key, as section 9.2 of the specification says.

```fern
<<8 the handshake state>>=
pub struct Config {
  protocol: Protocol,
  is_initiator: boolean,
  prologue: u8[],
  s: Option[KeyPair],
  rs: Option[u8[]],
  psks: u8[][],
  fixed_ephemeral: Option[KeyPair],
}

pub function initiator(protocol: Protocol, s: KeyPair): Config {
  return Config { protocol: protocol, is_initiator: true, prologue: [], s: Some(s), rs: None, psks: [], fixed_ephemeral: None };
}

pub function responder(protocol: Protocol, s: KeyPair): Config {
  return Config { protocol: protocol, is_initiator: false, prologue: [], s: Some(s), rs: None, psks: [], fixed_ephemeral: None };
}

@secret
pub opaque struct Handshake {
  is_initiator: boolean,
  messages: Token[][],
  index: i32,
  sym: Symmetric,
  has_psk: boolean,
  s: Option[KeyPair],
  e: Option[KeyPair],
  rs: Option[u8[]],
  re: Option[u8[]],
  psks: u8[][],
  next_psk: i32,
}

// If the local side writes an s token in a message of the pattern.
function __writes_static(messages: [Token[]], is_initiator: boolean): boolean {
  for i in 0..messages.len() {
    if (i % 2 == 0 == is_initiator) {
      for t in messages[i] {
        if let S = t {
          return true;
        }
      }
    }
  }
  return false;
}

function __check_key(what: string, key: [u8]): Result[void, NoiseError] {
  if (key.len() != KEY_SIZE) {
    return Err(KeyLength(what, key.len()));
  }
  return Ok(());
}

function __check_remote(rs: Option[u8[]], remote_pre: boolean): Result[void, NoiseError] {
  return match (rs) {
    Some(k) => if (remote_pre) { __check_key("remote static key", k) } else { Err(UnusedKey("remote static key")) },
    None => if (remote_pre) { Err(MissingKey("remote static key")) } else { Ok(()) },
  };
}

function __check_psks(psks: [u8[]], want: i32): Result[void, NoiseError] {
  if (psks.len() < want) {
    return Err(MissingKey("PSK"));
  }
  if (psks.len() > want) {
    return Err(UnusedKey("PSK"));
  }
  for psk in psks {
    __check_key("PSK", psk)?;
  }
  return Ok(());
}

function __check_config(c: Config, messages: [Token[]], pre_i: boolean, pre_r: boolean): Result[void, NoiseError] {
  let local_pre = if (c.is_initiator) { pre_i } else { pre_r };
  let remote_pre = if (c.is_initiator) { pre_r } else { pre_i };
  if ((local_pre || __writes_static(messages, c.is_initiator)) && c.s.is_none()) {
    return Err(MissingKey("local static key"));
  }
  __check_remote(c.rs, remote_pre)?;
  return __check_psks(c.psks, c.protocol.psks.len());
}

// The pre-messages go into the hash with the initiator's key first.
function __pre_hash(sym: Symmetric, c: Config, pre_i: boolean, pre_r: boolean): Symmetric {
  let local_key: u8[] = match (c.s) {
    Some(s) => s.public_key,
    None => [],
  };
  let remote_key: u8[] = c.rs.unwrap_or([]);
  let (initiator_key, responder_key) = if (c.is_initiator) { (local_key, remote_key) } else { (remote_key, local_key) };
  let out = sym;
  if (pre_i) {
    out = __mix_hash(out, initiator_key);
  }
  if (pre_r) {
    out = __mix_hash(out, responder_key);
  }
  return out;
}

pub function handshake(c: Config): Result[Handshake, NoiseError] {
  let messages = __with_psks(__messages(c.protocol.pattern), c.protocol.psks);
  let (pre_i, pre_r) = __pre(c.protocol.pattern);
  __check_config(c, messages, pre_i, pre_r)?;
  let sym = __pre_hash(__mix_hash(__symmetric(c.protocol), c.prologue), c, pre_i, pre_r);
  return Ok(Handshake {
    is_initiator: c.is_initiator,
    messages: messages,
    index: 0,
    sym: sym,
    has_psk: c.protocol.psks.len() > 0,
    s: c.s,
    e: c.fixed_ephemeral,
    rs: c.rs,
    re: None,
    psks: c.psks,
    next_psk: 0,
  });
}

pub function (hs: Handshake) is_finished(): boolean {
  return hs.index >= hs.messages.len();
}

// The hash of the handshake so far. After the last message, it identifies the session.
pub function (hs: Handshake) handshake_hash(): u8[] {
  return hs.sym.h;
}

// The static key of the peer, from a pre-message or from an s token.
pub function (hs: Handshake) remote_static(): Option[u8[]] {
  return hs.rs;
}

function __check_turn(hs: Handshake, writing: boolean): Result[void, NoiseError] {
  if (hs.index >= hs.messages.len()) {
    return Err(Finished);
  }
  if (hs.index % 2 == 0 == hs.is_initiator != writing) {
    return Err(Order);
  }
  return Ok(());
}

// The keys of the DH tokens. es is the initiator's e with the responder's s.
function __dh_keys(hs: Handshake, t: Token): (Option[KeyPair], Option[u8[]]) {
  return match (t) {
    EE => (hs.e, hs.re),
    SS => (hs.s, hs.rs),
    ES => if (hs.is_initiator) { (hs.e, hs.rs) } else { (hs.s, hs.re) },
    _ => if (hs.is_initiator) { (hs.s, hs.re) } else { (hs.e, hs.rs) },
  };
}

function __mix_dh(hs: Handshake, t: Token): Result[Symmetric, NoiseError] {
  let (local, remote) = __dh_keys(hs, t);
  let Some(k) = local else {
    return Err(MissingKey("local key"));
  };
  let Some(public_key) = remote else {
    return Err(MissingKey("remote key"));
  };
  let shared = __dh(k, public_key)?;
  let sym = __mix_key(hs.sym, shared);
  crypto.erase(shared);
  return Ok(sym);
}

// The state after one token. The index moves on only after the payload.
function __next(hs: Handshake, sym: Symmetric, e: Option[KeyPair], rs: Option[u8[]], re: Option[u8[]], next_psk: i32): Handshake {
  return Handshake {
    is_initiator: hs.is_initiator,
    messages: hs.messages,
    index: hs.index,
    sym: sym,
    has_psk: hs.has_psk,
    s: hs.s,
    e: e,
    rs: rs,
    re: re,
    psks: hs.psks,
    next_psk: next_psk,
  };
}

// An ephemeral public key goes into the hash, and with a PSK also into the key.
function __mix_ephemeral(hs: Handshake, public_key: [u8]): Symmetric {
  let sym = __mix_hash(hs.sym, public_key);
  if (hs.has_psk) {
    return __mix_key(sym, public_key);
  }
  return sym;
}

// The psk token and the DH tokens, which read and write no bytes.
function __mix_token(hs: Handshake, t: Token): Result[Handshake, NoiseError] {
  if let Psk = t {
    let sym = __mix_key_and_hash(hs.sym, hs.psks[hs.next_psk]);
    return Ok(__next(hs, sym, hs.e, hs.rs, hs.re, hs.next_psk + 1));
  }
  let sym = __mix_dh(hs, t)?;
  return Ok(__next(hs, sym, hs.e, hs.rs, hs.re, hs.next_psk));
}

function __write_token(hs: Handshake, t: Token): Result[(u8[], Handshake), NoiseError] {
  match (t) {
    E => {
      let e = match (hs.e) {
        Some(k) => k,
        None => generate_key_pair(),
      };
      let sym = __mix_ephemeral(hs, e.public_key);
      return Ok((e.public_key, __next(hs, sym, Some(e), hs.rs, hs.re, hs.next_psk)));
    },
    S => {
      let Some(s) = hs.s else {
        return Err(MissingKey("local static key"));
      };
      let (sealed, sym) = __encrypt_and_hash(hs.sym, s.public_key)?;
      return Ok((sealed, __next(hs, sym, hs.e, hs.rs, hs.re, hs.next_psk)));
    },
    _ => {
      return Ok(([], __mix_token(hs, t)?));
    }
  }
}

// The token at the offset of the message, and the offset after it.
function __read_token(hs: Handshake, t: Token, message: [u8], at: i32): Result[(i32, Handshake), NoiseError] {
  match (t) {
    E => {
      if (message.len() - at < KEY_SIZE) {
        return Err(Truncated);
      }
      let re = __take(message, at, KEY_SIZE);
      let sym = __mix_ephemeral(hs, re);
      return Ok((at + KEY_SIZE, __next(hs, sym, hs.e, hs.rs, Some(re), hs.next_psk)));
    },
    S => {
      let n = if (hs.sym.cs.k.len() == 0) { KEY_SIZE } else { KEY_SIZE + 16 };
      if (message.len() - at < n) {
        return Err(Truncated);
      }
      let (rs, sym) = __decrypt_and_hash(hs.sym, message[at:at + n])?;
      return Ok((at + n, __next(hs, sym, hs.e, Some(rs), hs.re, hs.next_psk)));
    },
    _ => {
      return Ok((at, __mix_token(hs, t)?));
    }
  }
}

pub function (own hs: Handshake) write_message(payload: [u8]): Result[(u8[], Handshake), NoiseError] {
  __check_turn(hs, true)?;
  let st = hs;
  let out: u8[] = [];
  for t in st.messages[st.index] {
    let (bytes, next) = __write_token(st, t)?;
    out = out.concat(bytes);
    st = next;
  }
  let (sealed, sym) = __encrypt_and_hash(st.sym, payload)?;
  out = out.concat(sealed);
  if (out.len() > MAX_MESSAGE) {
    return Err(MessageLength(out.len()));
  }
  let next = __next(st, sym, st.e, st.rs, st.re, st.next_psk);
  return Ok((out, Handshake { ...next, index: next.index + 1 }));
}

pub function (own hs: Handshake) read_message(message: [u8]): Result[(u8[], Handshake), NoiseError] {
  __check_turn(hs, false)?;
  if (message.len() > MAX_MESSAGE) {
    return Err(MessageLength(message.len()));
  }
  let st = hs;
  let at = 0;
  for t in st.messages[st.index] {
    let (after, next) = __read_token(st, t, message, at)?;
    at = after;
    st = next;
  }
  let (payload, sym) = __decrypt_and_hash(st.sym, message[at:message.len()])?;
  let next = __next(st, sym, st.e, st.rs, st.re, st.next_psk);
  return Ok((payload, Handshake { ...next, index: next.index + 1 }));
}

// The transport state after the last message. The initiator sends with the
// first key and the responder with the second.
pub function (own hs: Handshake) split(): Result[Transport, NoiseError] {
  if (hs.index < hs.messages.len()) {
    return Err(Order);
  }
  let out = __hkdf(hs.sym.ck, [], 2);
  let c1 = CipherState { cipher: hs.sym.cs.cipher, k: __take(out, 0, 32), n: 0 as u64 };
  let c2 = CipherState { cipher: hs.sym.cs.cipher, k: __take(out, 32, 32), n: 0 as u64 };
  crypto.erase(out);
  if (hs.is_initiator) {
    return Ok(Transport { send: c1, receive: c2, handshake_hash: hs.sym.h, rs: hs.rs });
  }
  return Ok(Transport { send: c2, receive: c1, handshake_hash: hs.sym.h, rs: hs.rs });
}
```

### `9 the transport state`

A transport message has no associated data. `write` refuses a payload that makes a message longer than 65535 bytes, and `read` refuses a longer input.

```fern
<<9 the transport state>>=
@secret
pub opaque struct Transport {
  send: CipherState,
  receive: CipherState,
  handshake_hash: u8[],
  rs: Option[u8[]],
}

pub function (t: Transport) handshake_hash(): u8[] {
  return t.handshake_hash;
}

pub function (t: Transport) remote_static(): Option[u8[]] {
  return t.rs;
}

pub function (own t: Transport) write(payload: [u8]): Result[(u8[], Transport), NoiseError] {
  if (payload.len() > MAX_MESSAGE - 16) {
    return Err(MessageLength(payload.len() + 16));
  }
  let (sealed, send) = __encrypt_with_ad(t.send, [], payload)?;
  return Ok((sealed, Transport { send: send, receive: t.receive, handshake_hash: t.handshake_hash, rs: t.rs }));
}

pub function (own t: Transport) read(message: [u8]): Result[(u8[], Transport), NoiseError] {
  if (message.len() > MAX_MESSAGE) {
    return Err(MessageLength(message.len()));
  }
  let (opened, receive) = __decrypt_with_ad(t.receive, [], message)?;
  return Ok((opened, Transport { send: t.send, receive: receive, handshake_hash: t.handshake_hash, rs: t.rs }));
}
```
