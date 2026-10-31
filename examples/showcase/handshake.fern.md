# Post-quantum handshake

The browser and the native server agree on a session key with a hybrid of two key exchanges.
ML-KEM-768 resists a quantum attacker. X25519 keeps the protection of a classical exchange if a flaw appears in the newer one.
The session key depends on both shared secrets, so an attacker must break both. TLS uses the same X25519MLKEM768 hybrid.

The browser runs its half as Fern compiled to WebAssembly. The server runs this same module natively.
Neither side sends a secret. The browser sends public keys, and the server returns a ciphertext, its public key, a confirmation, and a sealed message.

```fern
<<*>>=
<<Imports>>
<<Key schedule>>
<<Client>>
<<Server>>
<<Command entry>>
```

## Imports

```fern
<<Imports>>=
import "std/crypto";
import "std/crypto/mlkem768";
import "std/crypto/x25519";
import "std/crypto/chacha20poly1305" as chacha;
import "std/array";
import "std/string";
```

## Key schedule

The transcript is the SHA-256 hash of every public value in order: the ML-KEM encapsulation key, both X25519 public keys, and the ciphertext.
HMAC-SHA-256 with the transcript as its key combines the two shared secrets. A change to any public value therefore changes the session key.

The server proves that it holds the key with an HMAC over a fixed label. The sealed message uses ChaCha20-Poly1305 with the transcript as associated data.

```fern
<<Key schedule>>=
pub function transcript(ek: u8[], client_x: u8[], ciphertext: u8[], server_x: u8[]): u8[] {
  return crypto.sha256_new().update(ek).update(client_x).update(ciphertext).update(server_x).finish();
}

pub function session_key(kem: u8[], classical: u8[], transcript: u8[]): u8[] {
  return crypto.hmac_sha256(transcript, kem.concat(classical));
}

@export("fern:showcase/handshake", "confirmation")
pub function confirmation(key: u8[]): u8[] {
  return crypto.hmac_sha256(key, "fern server finished".bytes());
}
```

## Client

The page supplies 96 random bytes from `crypto.getRandomValues`: two 32-byte ML-KEM seeds and an X25519 scalar.
The kernel itself needs no random source. Each export returns an empty list for invalid input.

`client-keys` returns the encapsulation key (1184 bytes), the decapsulation key (2400 bytes), and the X25519 public key (32 bytes).
`client-session` returns the session key and the transcript, 32 bytes each.

```fern
<<Client>>=
@export("fern:showcase/handshake", "client-keys")
pub function client_keys(seed: u8[]): u8[] {
  if (seed.len() != 96) {
    return [];
  }
  let Ok(pair) = mlkem768.keygen_derand(seed.take(32), seed.drop(32).take(32)) else {
    return [];
  };
  let Ok(public) = x25519.public_key(seed.drop(64)) else {
    return [];
  };
  return pair.encapsulation_key.concat(pair.decapsulation_key).concat(public);
}

@export("fern:showcase/handshake", "client-session")
pub function client_session(dk: u8[], scalar: u8[], ek: u8[], client_x: u8[], ciphertext: u8[], server_x: u8[]): u8[] {
  let Ok(kem) = mlkem768.decaps(dk, ciphertext) else {
    return [];
  };
  let Ok(classical) = x25519.x25519(scalar, server_x) else {
    return [];
  };
  let record = transcript(ek, client_x, ciphertext, server_x);
  return session_key(kem, classical, record).concat(record);
}

@export("fern:showcase/handshake", "open")
pub function open(key: u8[], record: u8[], nonce: u8[], sealed: u8[]): u8[] {
  let Ok(cipher) = chacha.key(key) else {
    return [];
  };
  let Ok(plain) = cipher.open(nonce, sealed, record) else {
    return [];
  };
  return plain;
}
```

## Server

The server takes its random values as parameters: the ML-KEM message, the X25519 scalar, and the nonce.
The HTTP handler supplies them from the operating system. A test can supply fixed values.

```fern
<<Server>>=
pub struct Reply { ciphertext: u8[], server_x: u8[], confirmation: u8[], nonce: u8[], sealed: u8[] }

pub function reply(ek: u8[], client_x: u8[], message: u8[], scalar: u8[], nonce: u8[], text: u8[]): Result[Reply, string] {
  let Ok(sent) = mlkem768.encaps_derand(ek, message) else {
    return Err("The encapsulation key is not valid ML-KEM-768.");
  };
  let Ok(server_x) = x25519.public_key(scalar) else {
    return Err("The server scalar is not valid.");
  };
  let Ok(classical) = x25519.x25519(scalar, client_x) else {
    return Err("The X25519 public key is not valid.");
  };
  let record = transcript(ek, client_x, sent.ciphertext, server_x);
  let key = session_key(sent.shared_secret, classical, record);
  let Ok(cipher) = chacha.key(key) else {
    return Err("The session key is not valid.");
  };
  let Ok(sealed) = cipher.seal(nonce, text, record) else {
    return Err("The nonce is not valid.");
  };
  return Ok(Reply { ciphertext: sent.ciphertext, server_x: server_x, confirmation: confirmation(key), nonce: nonce, sealed: sealed });
}
```

## Command entry

```fern
<<Command entry>>=
function main(): i32 {
  return 0;
}
```

## Check the behavior

Both sides derive the same key from fixed random values. The client confirms the server and opens its message. A changed ciphertext fails.

```fern test name=handshake-example
<<Imports>>
<<Key schedule>>
<<Client>>
<<Server>>
function fill(value: u8, count: i32): u8[] {
  let out: u8[] = [];
  for i in 0..count {
    out = out.append(value + i as u8);
  }
  return out;
}
function main(): i32 {
  let keys = client_keys(fill(1, 96));
  if (keys.len() != 1184 + 2400 + 32) { return 1; }
  let ek = keys.take(1184);
  let dk = keys.drop(1184).take(2400);
  let client_x = keys.drop(3584);
  let Ok(answer) = reply(ek, client_x, fill(7, 32), fill(9, 32), fill(3, 12), "hello".bytes()) else { return 2; };
  let session = client_session(dk, fill(1, 96).drop(64), ek, client_x, answer.ciphertext, answer.server_x);
  let key = session.take(32);
  if (!crypto.consteq(confirmation(key), answer.confirmation)) { return 3; }
  if (!crypto.consteq(open(key, session.drop(32), answer.nonce, answer.sealed), "hello".bytes())) { return 4; }
  let damaged = answer.ciphertext.with(0, answer.ciphertext[0] ^ 1);
  if (crypto.consteq(client_session(dk, fill(1, 96).drop(64), ek, client_x, damaged, answer.server_x).take(32), key)) { return 5; }
  return 0;
}
```
