# TLS 1.3 key shares

This module provides TLS 1.3 key agreement operations.

## Model

Group values describe supported key-agreement algorithms. Each group supplies wire identifiers, entropy sizes, and public-share sizes.

## Contracts

Client and server entropy contracts can differ. Invalid share lengths or unsupported groups produce a concrete key-share error.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum Group>>

<<4 pub function (g: Group) code(): i32>>

<<5 pub enum KeyShareError>>

<<6 pub function (e: KeyShareError) to_string(): string>>

<<7 impl error.Error for KeyShareError>>

<<8 pub function (e: KeyShareError) message(): string>>

<<9 const P256_ORDER: string>>

<<10 function __p256_key(entropy: [u8]): Result[ecdsa.PrivateKey, KeyShareError]>>

<<11 pub struct ClientShare>>

<<12 function __cat(a: [u8], b: [u8]): u8[]>>

<<13 pub function client_share(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError]>>

<<14 function __x25519_client(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError]>>

<<15 pub function server_share(g: Group, client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError]>>

<<16 function __x25519_server(client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError]>>

<<17 pub function (c: ClientShare) shared_secret(server_public: [u8]): Result[u8[], KeyShareError]>>

<<18 function __x25519_hybrid_secret(c: ClientShare, server_public: [u8]): Result[u8[], KeyShareError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides TLS 1.3 key agreement operations.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/crypto/ecdsa";
import "std/crypto/mlkem768";
import "std/crypto/montgomery";
import "std/crypto/x25519";
import "std/error";
import "core/cmp";
```

### `3 pub enum Group`

```fern
<<3 pub enum Group>>=
pub enum Group {
  X25519,
  X25519MlKem768,
  Secp256r1,
  Secp256r1MlKem768,
}
```

### `4 pub function (g: Group) code(): i32`

```fern
<<4 pub function (g: Group) code(): i32>>=
pub function (g: Group) code(): i32 {
  match (g) {
    X25519 => {
      return 0x001d;
    },
    X25519MlKem768 => {
      return 0x11ec;
    },
    Secp256r1 => {
      return 0x0017;
    },
    Secp256r1MlKem768 => {
      return 0x11eb;
    }
  }
  return 0;
}

pub function group(code: i32): Option[Group] {
  if (code == 0x001d) {
    return Some(X25519);
  }
  if (code == 0x11ec) {
    return Some(X25519MlKem768);
  }
  if (code == 0x0017) {
    return Some(Secp256r1);
  }
  if (code == 0x11eb) {
    return Some(Secp256r1MlKem768);
  }
  return None;
}

pub function (g: Group) client_entropy(): i32 {
  match (g) {
    X25519 => {
      return 32;
    },
    X25519MlKem768 => {
      return 96;
    },
    Secp256r1 => {
      return 40;
    },
    Secp256r1MlKem768 => {
      return 104;
    }
  }
  return 0;
}

pub function (g: Group) server_entropy(): i32 {
  match (g) {
    X25519 => {
      return 32;
    },
    X25519MlKem768 => {
      return 64;
    },
    Secp256r1 => {
      return 40;
    },
    Secp256r1MlKem768 => {
      return 72;
    }
  }
  return 0;
}

pub function (g: Group) client_share_len(): i32 {
  match (g) {
    X25519 => {
      return 32;
    },
    X25519MlKem768 => {
      return 1216;
    },
    Secp256r1 => {
      return 65;
    },
    Secp256r1MlKem768 => {
      return 1249;
    }
  }
  return 0;
}

pub function (g: Group) server_share_len(): i32 {
  match (g) {
    X25519 => {
      return 32;
    },
    X25519MlKem768 => {
      return 1120;
    },
    Secp256r1 => {
      return 65;
    },
    Secp256r1MlKem768 => {
      return 1153;
    }
  }
  return 0;
}
```

### `5 pub enum KeyShareError`

```fern
<<5 pub enum KeyShareError>>=
pub enum KeyShareError {
  EntropyLength(i32, i32),
  ShareLength(i32, i32),
  LowOrder,
  BadEncapsulationKey,
  OffCurve,
  ZeroScalar,
}
```

### `6 pub function (e: KeyShareError) to_string(): string`

```fern
<<6 pub function (e: KeyShareError) to_string(): string>>=
pub function (e: KeyShareError) to_string(): string {
  return e.message();
}
```

### `7 impl error.Error for KeyShareError`

```fern
<<7 impl error.Error for KeyShareError>>=
impl error.Error for KeyShareError {}

impl cmp.Display for KeyShareError {}
```

### `8 pub function (e: KeyShareError) message(): string`

```fern
<<8 pub function (e: KeyShareError) message(): string>>=
pub function (e: KeyShareError) message(): string {
  match (e) {
    EntropyLength(n, want) => {
      return "tls keyshare: " + int.int_to_string(n) + " bytes of entropy, want " + int.int_to_string(want);
    },
    ShareLength(n, want) => {
      return "tls keyshare: the peer's share is " + int.int_to_string(n) + " bytes, want " + int.int_to_string(want);
    },
    LowOrder => {
      return "tls keyshare: the peer's X25519 key is of low order";
    },
    BadEncapsulationKey => {
      return "tls keyshare: the peer's ML-KEM encapsulation key is not reduced";
    },
    OffCurve => {
      return "tls keyshare: the peer's secp256r1 point is not an uncompressed point on the curve";
    },
    ZeroScalar => {
      return "tls keyshare: the secp256r1 draw reduces to zero";
    }
  }
  return "";
}
```

### `9 const P256_ORDER: string`

```fern
<<9 const P256_ORDER: string>>=
const P256_ORDER: string = "ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551";

const P256_R256: u8[] = [255, 255, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0, 67, 25, 5, 82, 88, 232, 97, 123, 12, 70, 53, 61, 3, 156, 218, 175];
```

### `10 function __p256_key(entropy: [u8]): Result[ecdsa.PrivateKey, KeyShareError]`

```fern
<<10 function __p256_key(entropy: [u8]): Result[ecdsa.PrivateKey, KeyShareError]>>=
function __p256_key(entropy: [u8]): Result[ecdsa.PrivateKey, KeyShareError] {
  let order: montgomery.Modulus = montgomery.modulus(P256_ORDER, 32);
  let hi: i64[] = montgomery.mul(order, montgomery.from_bytes(order, entropy[0:8]), montgomery.from_bytes(order, P256_R256));
  let d: i64[] = montgomery.add(order, hi, montgomery.from_bytes(order, entropy[8:40]));
  match (ecdsa.private_key(ecdsa.P256, montgomery.to_bytes(order, d))) {
    Ok(k) => {
      return Ok(k);
    },
    Err(e) => {
      return Err(ZeroScalar);
    }
  }
  return Err(ZeroScalar);
}

function __p256_agree(k: ecdsa.PrivateKey, peer: [u8]): Result[u8[], KeyShareError] {
  match (k.ecdh(peer)) {
    Ok(shared) => {
      return Ok(shared);
    },
    Err(e) => {
      return Err(OffCurve);
    }
  }
  return Err(OffCurve);
}
```

### `11 pub struct ClientShare`

```fern
<<11 pub struct ClientShare>>=
pub struct ClientShare { group: Group, public: u8[], scalar: u8[], decapsulation_key: u8[] }

pub struct ServerShare { public: u8[], shared_secret: u8[] }
```

### `12 function __cat(a: [u8], b: [u8]): u8[]`

```fern
<<12 function __cat(a: [u8], b: [u8]): u8[]>>=
function __cat(a: [u8], b: [u8]): u8[] {
  let out: u8[] = __alloc_u8(a.len() + b.len());
  let i: i32 = 0;
  while (i < a.len()) {
    out = out.with(i, a[i]);
    i = i + 1;
  }
  let j: i32 = 0;
  while (j < b.len()) {
    out = out.with(a.len() + j, b[j]);
    j = j + 1;
  }
  return out;
}

function __x25519_error(e: x25519.X25519Error): KeyShareError {
  match (e) {
    x25519.ScalarLength(n) => {
      return EntropyLength(n, 32);
    },
    x25519.PointLength(n) => {
      return ShareLength(n, 32);
    },
    x25519.LowOrder => {
      return KeyShareError.LowOrder;
    }
  }
  return KeyShareError.LowOrder;
}

function __mlkem_error(e: mlkem768.MlKemError): KeyShareError {
  return BadEncapsulationKey;
}

function __public(scalar: [u8]): Result[u8[], KeyShareError] {
  match (x25519.public_key(scalar)) {
    Ok(p) => {
      return Ok(p);
    },
    Err(e) => {
      return Err(__x25519_error(e));
    }
  }
  return Err(KeyShareError.LowOrder);
}

function __agree(scalar: [u8], point: [u8]): Result[u8[], KeyShareError] {
  match (x25519.x25519(scalar, point)) {
    Ok(s) => {
      return Ok(s);
    },
    Err(e) => {
      return Err(__x25519_error(e));
    }
  }
  return Err(KeyShareError.LowOrder);
}
```

### `13 pub function client_share(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError]`

```fern
<<13 pub function client_share(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError]>>=
pub function client_share(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError] {
  if (entropy.len() != g.client_entropy()) {
    return Err(EntropyLength(entropy.len(), g.client_entropy()));
  }
  match (g) {
    X25519 => {
      return __x25519_client(g, entropy);
    },
    X25519MlKem768 => {
      return __x25519_hybrid_client(g, entropy);
    },
    Secp256r1 => {
      return __p256_client(g, entropy);
    },
    Secp256r1MlKem768 => {
      return __p256_hybrid_client(g, entropy);
    }
  }
  return Err(EntropyLength(entropy.len(), g.client_entropy()));
}
```

### `14 function __x25519_client(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError]`

```fern
<<14 function __x25519_client(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError]>>=
function __x25519_client(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError] {
  let scalar: u8[] = __cat(entropy[0:32], []);
  return Ok(ClientShare { group: g, public: __public(scalar)?, scalar: scalar, decapsulation_key: [] });
}

function __x25519_hybrid_client(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError] {
  let scalar: u8[] = __cat(entropy[0:32], []);
  let point: u8[] = __public(scalar)?;
  match (mlkem768.keygen_derand(entropy[32:64], entropy[64:96])) {
    Ok(kp) => {
      return Ok(ClientShare { group: g, public: __cat(kp.encapsulation_key, point), scalar: scalar, decapsulation_key: kp.decapsulation_key });
    },
    Err(e) => {
      return Err(__mlkem_error(e));
    }
  }
  return Err(BadEncapsulationKey);
}

function __p256_client(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError] {
  let k: ecdsa.PrivateKey = __p256_key(entropy)?;
  return Ok(ClientShare { group: g, public: k.public_point(), scalar: k.d, decapsulation_key: [] });
}

function __p256_hybrid_client(g: Group, entropy: [u8]): Result[ClientShare, KeyShareError] {
  let k: ecdsa.PrivateKey = __p256_key(entropy[0:40])?;
  match (mlkem768.keygen_derand(entropy[40:72], entropy[72:104])) {
    Ok(kp) => {
      return Ok(ClientShare { group: g, public: __cat(k.public_point(), kp.encapsulation_key), scalar: k.d, decapsulation_key: kp.decapsulation_key });
    },
    Err(e) => {
      return Err(__mlkem_error(e));
    }
  }
  return Err(BadEncapsulationKey);
}
```

### `15 pub function server_share(g: Group, client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError]`

```fern
<<15 pub function server_share(g: Group, client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError]>>=
pub function server_share(g: Group, client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError] {
  if (entropy.len() != g.server_entropy()) {
    return Err(EntropyLength(entropy.len(), g.server_entropy()));
  }
  if (client_public.len() != g.client_share_len()) {
    return Err(ShareLength(client_public.len(), g.client_share_len()));
  }
  match (g) {
    X25519 => {
      return __x25519_server(client_public, entropy);
    },
    X25519MlKem768 => {
      return __x25519_hybrid_server(client_public, entropy);
    },
    Secp256r1 => {
      return __p256_server(client_public, entropy);
    },
    Secp256r1MlKem768 => {
      return __p256_hybrid_server(client_public, entropy);
    }
  }
  return Err(ShareLength(client_public.len(), g.client_share_len()));
}
```

### `16 function __x25519_server(client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError]`

```fern
<<16 function __x25519_server(client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError]>>=
function __x25519_server(client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError] {
  let scalar: u8[] = __cat(entropy[0:32], []);
  return Ok(ServerShare { public: __public(scalar)?, shared_secret: __agree(scalar, client_public)? });
}

function __x25519_hybrid_server(client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError] {
  let scalar: u8[] = __cat(entropy[0:32], []);
  let point: u8[] = __public(scalar)?;
  match (mlkem768.encaps_derand(client_public[0:1184], entropy[32:64])) {
    Ok(enc) => {
      let ss: u8[] = __agree(scalar, client_public[1184:1216])?;
      return Ok(ServerShare { public: __cat(enc.ciphertext, point), shared_secret: __cat(enc.shared_secret, ss) });
    },
    Err(e) => {
      return Err(__mlkem_error(e));
    }
  }
  return Err(BadEncapsulationKey);
}

function __p256_server(client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError] {
  let k: ecdsa.PrivateKey = __p256_key(entropy)?;
  return Ok(ServerShare { public: k.public_point(), shared_secret: __p256_agree(k, client_public)? });
}

function __p256_hybrid_server(client_public: [u8], entropy: [u8]): Result[ServerShare, KeyShareError] {
  let k: ecdsa.PrivateKey = __p256_key(entropy[0:40])?;
  let ss: u8[] = __p256_agree(k, client_public[0:65])?;
  match (mlkem768.encaps_derand(client_public[65:1249], entropy[40:72])) {
    Ok(enc) => {
      return Ok(ServerShare { public: __cat(k.public_point(), enc.ciphertext), shared_secret: __cat(ss, enc.shared_secret) });
    },
    Err(e) => {
      return Err(__mlkem_error(e));
    }
  }
  return Err(BadEncapsulationKey);
}
```

### `17 pub function (c: ClientShare) shared_secret(server_public: [u8]): Result[u8[], KeyShareError]`

```fern
<<17 pub function (c: ClientShare) shared_secret(server_public: [u8]): Result[u8[], KeyShareError]>>=
pub function (c: ClientShare) shared_secret(server_public: [u8]): Result[u8[], KeyShareError] {
  if (server_public.len() != c.group.server_share_len()) {
    return Err(ShareLength(server_public.len(), c.group.server_share_len()));
  }
  match (c.group) {
    X25519 => {
      return __agree(c.scalar, server_public);
    },
    X25519MlKem768 => {
      return __x25519_hybrid_secret(c, server_public);
    },
    Secp256r1 => {
      return __p256_agree(__p256_scalar(c.scalar)?, server_public);
    },
    Secp256r1MlKem768 => {
      return __p256_hybrid_secret(c, server_public);
    }
  }
  return Err(ShareLength(server_public.len(), c.group.server_share_len()));
}
```

### `18 function __x25519_hybrid_secret(c: ClientShare, server_public: [u8]): Result[u8[], KeyShareError]`

```fern
<<18 function __x25519_hybrid_secret(c: ClientShare, server_public: [u8]): Result[u8[], KeyShareError]>>=
function __x25519_hybrid_secret(c: ClientShare, server_public: [u8]): Result[u8[], KeyShareError] {
  match (mlkem768.decaps(c.decapsulation_key, server_public[0:1088])) {
    Ok(k) => {
      return Ok(__cat(k, __agree(c.scalar, server_public[1088:1120])?));
    },
    Err(e) => {
      return Err(__mlkem_error(e));
    }
  }
  return Err(BadEncapsulationKey);
}

function __p256_hybrid_secret(c: ClientShare, server_public: [u8]): Result[u8[], KeyShareError] {
  let ss: u8[] = __p256_agree(__p256_scalar(c.scalar)?, server_public[0:65])?;
  match (mlkem768.decaps(c.decapsulation_key, server_public[65:1153])) {
    Ok(kem) => {
      return Ok(__cat(ss, kem));
    },
    Err(e) => {
      return Err(__mlkem_error(e));
    }
  }
  return Err(BadEncapsulationKey);
}

function __p256_scalar(scalar: [u8]): Result[ecdsa.PrivateKey, KeyShareError] {
  match (ecdsa.private_key(ecdsa.P256, scalar)) {
    Ok(k) => {
      return Ok(k);
    },
    Err(e) => {
      return Err(ZeroScalar);
    }
  }
  return Err(ZeroScalar);
}
```
