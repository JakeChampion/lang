# Certificate and signature verification

This module validates certificate paths, host names, and TLS signatures.

## Model

Verification combines trust roots, certificate paths, host names, time, and signature rules. Root metadata can include a distrust time.

## Contracts

A parsed certificate does not confer trust. The verifier must evaluate the requested peer role and name against the selected roots and applicable certificate constraints.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 const MAX_PATH: i32>>

<<4 pub function (e: VerifyError) to_string(): string>>

<<5 impl error.Error for VerifyError>>

<<6 pub function (e: VerifyError) message(): string>>

<<7 pub struct Roots>>

<<8 pub function (r: Roots) distrusting(i: i32, after: i64): Roots>>

<<9 const BUNDLES: string[]>>

<<10 pub function roots_from_file(path: string): Result[Roots, VerifyError]>>

<<11 function __signed_by(c: x509.Certificate, issuer: x509.Certificate): Result[boolean, VerifyError]>>

<<12 pub function signature_ok(key: x509.PublicKey, alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, ...>>

<<13 function __rsa(n: u8[], e: u8[], alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, VerifyError]>>

<<14 pub function scheme_fits(cert: x509.Certificate, scheme: i32): boolean>>

<<15 function __usable(c: x509.Certificate, at: i32, now: i64): Result[boolean, VerifyError]>>

<<16 struct Search>>

<<17 function __extend(below: x509.Certificate[], at: i32, inter: x509.Certificate[], roots: Roots, now: i64, se...>>

<<18 function __leaf_ok(leaf: x509.Certificate, name: string, now: i64): Result[boolean, VerifyError]>>

<<19 pub function verify_server(chain: u8[][], roots: Roots, name: string, now: i64): Result[x509.Certificate, V...>>

<<20 function __parsed(chain: u8[][]): Result[x509.Certificate[], VerifyError]>>

<<21 pub function denied_key(c: x509.Certificate): boolean>>

<<22 function __rooted(certs: x509.Certificate[], roots: Roots, now: i64, server: boolean): Result[x509.Certific...>>

<<23 pub function matches_name(c: x509.Certificate, name: string): boolean>>

<<24 function __normal(name: string): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module validates certificate paths, host names, and TLS signatures.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/crypto";
import "std/crypto/ed25519";
import "std/crypto/ecdsa";
import "std/crypto/sha2";
import "std/crypto/rsa";
import "std/hex";
import "std/net";
import "std/string";
import "std/tls/pem";
import "std/tls/x509";
import "std/error";
import "core/cmp";
```

### `3 const MAX_PATH: i32`

```fern
<<3 const MAX_PATH: i32>>=
const MAX_PATH: i32 = 8;

pub const DENIED_SPKI: string[] = ["ebed6475c2c2d80fcb199f3fe8dd6e6b3ce60b77eb15d4e42c3dec0c6f5b5105", "49dd353dc2b919a3770645117d0e60c16115e7d1bc259eefe734b00ea434fe5f", "e0254c7fcdc265e2f0f8a428dabb75ab4ae039b4872d62f5ee57f0877d5a8a41", "5c0660a2b164910d8ff589c18e14552e9e0ec237dcefe2cb736d41a4811f81a0", "43bffa19d6ef651451f6841e019931e96f6f2eaeff28ab4b6f1ce4576de2717b", "63ecefee431d49523bc1136c4e72d0206a477086caf606cd5cf025686c109cf8", "ec4ffa1ad0ac88081314b7aaffef6a91f97cd57291bb5ba686c5415f5eb4f6b6", "92c063d67e8f14db71d288e11266ffdaf69cbf115f7bab14f3bc2f68d25e0e30"];

pub enum VerifyError {
  EmptyChain,
  Parse(i32, x509.X509Error),
  Pem(pem.PemError),
  Expired(i32),
  NotYetValid(i32),
  UnknownCritical(i32, string),
  UnknownIssuer,
  NotACa(i32),
  PathTooLong(i32),
  KeyUsage(i32),
  NotForServers,
  NotForClients,
  NameMismatch(string),
  BadSignature(i32),
  NameConstraint(i32),
  RootsUnreadable(string),
  UnsupportedAlgorithm(string),
  Distrusted(i32),
  DeniedKey(i32),
}
```

### `4 pub function (e: VerifyError) to_string(): string`

```fern
<<4 pub function (e: VerifyError) to_string(): string>>=
pub function (e: VerifyError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for VerifyError`

```fern
<<5 impl error.Error for VerifyError>>=
impl error.Error for VerifyError {}

impl cmp.Display for VerifyError {}
```

### `6 pub function (e: VerifyError) message(): string`

```fern
<<6 pub function (e: VerifyError) message(): string>>=
// fern-lint: allow cyclomatic-complexity
pub function (e: VerifyError) message(): string {
  match (e) {
    EmptyChain => {
      return "tls verify: the chain is empty";
    },
    Parse(i, x) => {
      return "tls verify: certificate " + int.int_to_string(i) + ": " + x.message();
    },
    Pem(p) => {
      return p.message();
    },
    Expired(i) => {
      return "tls verify: certificate " + int.int_to_string(i) + " has expired";
    },
    NotYetValid(i) => {
      return "tls verify: certificate " + int.int_to_string(i) + " is not yet valid";
    },
    Distrusted(i) => {
      return "tls verify: the leaf was issued after the date root " + int.int_to_string(i) + " is distrusted from";
    },
    UnknownCritical(i, oid) => {
      return "tls verify: certificate " + int.int_to_string(i) + " has an unknown critical extension " + oid;
    },
    UnknownIssuer => {
      return "tls verify: no path from the certificate reaches a trusted root";
    },
    NotACa(i) => {
      return "tls verify: certificate " + int.int_to_string(i) + " issues certificates but is not a CA";
    },
    PathTooLong(i) => {
      return "tls verify: certificate " + int.int_to_string(i) + " allows a shorter path";
    },
    KeyUsage(i) => {
      return "tls verify: certificate " + int.int_to_string(i) + "'s key usage does not allow this";
    },
    NotForServers => {
      return "tls verify: the certificate is not for TLS servers";
    },
    NotForClients => {
      return "tls verify: the certificate is not for TLS clients";
    },
    NameMismatch(name) => {
      return "tls verify: the certificate is not valid for " + name;
    },
    BadSignature(i) => {
      return "tls verify: certificate " + int.int_to_string(i) + "'s signature does not verify";
    },
    RootsUnreadable(path) => {
      return "tls verify: no certificates could be read from " + path;
    },
    NameConstraint(i) => {
      return "tls verify: certificate " + int.int_to_string(i) + "'s name constraints exclude a name below it";
    },
    UnsupportedAlgorithm(what) => {
      return "tls verify: unsupported " + what;
    },
    DeniedKey(i) => {
      return "tls verify: certificate " + int.int_to_string(i) + "'s key is known to be compromised";
    }
  }
  return "";
}
```

### `7 pub struct Roots`

```fern
<<7 pub struct Roots>>=
pub struct Roots { certs: x509.Certificate[], distrust_after: i64[] }
```

### `8 pub function (r: Roots) distrusting(i: i32, after: i64): Roots`

```fern
<<8 pub function (r: Roots) distrusting(i: i32, after: i64): Roots>>=
pub function (r: Roots) distrusting(i: i32, after: i64): Roots {
  return Roots { ...r, distrust_after: r.distrust_after.with(i, after) };
}

pub function no_roots(): Roots {
  return Roots { certs: [], distrust_after: [] };
}

pub function roots(ders: u8[][]): Roots {
  let out: x509.Certificate[] = [];
  for d in ders {
    if let Ok(c) = x509.parse(d) {
      out = out.append(c);
    }
  }
  let dates: i64[] = [];
  for c in out {
    dates = dates.append(0 as i64);
  }
  return Roots { certs: out, distrust_after: dates };
}

pub function roots_from_pem(text: string): Result[Roots, VerifyError] {
  match (pem.certificates(text)) {
    Ok(ders) => {
      return Ok(roots(ders));
    },
    Err(e) => {
      return Err(Pem(e));
    }
  }
  return Ok(no_roots());
}
```

### `9 const BUNDLES: string[]`

```fern
<<9 const BUNDLES: string[]>>=
const BUNDLES: string[] = ["/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/ca-bundle.pem", "/etc/pki/tls/cacert.pem", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", "/etc/ssl/cert.pem"];
```

### `10 pub function roots_from_file(path: string): Result[Roots, VerifyError]`

```fern
<<10 pub function roots_from_file(path: string): Result[Roots, VerifyError]>>=
pub function roots_from_file(path: string): Result[Roots, VerifyError] {
  match (read_file(path)) {
    Ok(text) => {
      let r: Roots = roots_from_pem(text)?;
      if (r.certs.len() > 0) {
        return Ok(r);
      }
    },
    Err(_) => {}
  }
  return Err(RootsUnreadable(path));
}

pub function system_roots(): Option[Roots] {
  let paths: string[] = BUNDLES;
  if let Some(p) = env("SSL_CERT_FILE") {
    paths = [p];
  }
  for p in paths {
    if let Ok(r) = roots_from_file(p) {
      return Some(r);
    }
  }
  return None;
}
```

### `11 function __signed_by(c: x509.Certificate, issuer: x509.Certificate): Result[boolean, VerifyError]`

```fern
<<11 function __signed_by(c: x509.Certificate, issuer: x509.Certificate): Result[boolean, VerifyError]>>=
function __signed_by(c: x509.Certificate, issuer: x509.Certificate): Result[boolean, VerifyError] {
  return signature_ok(issuer.key, c.algorithm, c.tbs, c.signature);
}
```

### `12 pub function signature_ok(key: x509.PublicKey, alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, ...`

```fern
<<12 pub function signature_ok(key: x509.PublicKey, alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, ...>>=
pub function signature_ok(key: x509.PublicKey, alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, VerifyError] {
  match (key) {
    x509.RsaKey(n, e) => {
      return __rsa(n, e, alg, msg, sig);
    },
    x509.EcKey(curve, point) => {
      return __ecdsa(curve, point, alg, msg, sig);
    },
    x509.Ed25519Key(k) => {
      if (x509.algorithm_name(alg) == "ed25519") {
        match (ed25519.public_key(k)) {
          Ok(pk) => {
            return Ok(pk.verify(msg, sig));
          },
          Err(e) => {
            return Err(UnsupportedAlgorithm("Ed25519 key: " + e.message()));
          }
        }
      }
    },
    x509.OtherKey(oid) => {
      return Err(UnsupportedAlgorithm("key " + oid));
    }
  }
  return Err(UnsupportedAlgorithm(x509.algorithm_name(alg) + " under an Ed25519 key"));
}
```

### `13 function __rsa(n: u8[], e: u8[], alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, VerifyError]`

```fern
<<13 function __rsa(n: u8[], e: u8[], alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, VerifyError]>>=
function __rsa(n: u8[], e: u8[], alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, VerifyError] {
  match (rsa.public_key(n, e)) {
    Ok(k) => {
      return __rsa_check(k, alg, msg, sig);
    },
    Err(err) => {
      return Err(UnsupportedAlgorithm("RSA key: " + err.message()));
    }
  }
  return Err(UnsupportedAlgorithm("RSA key"));
}

function __rsa_check(key: rsa.PublicKey, alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, VerifyError] {
  let name: string = x509.algorithm_name(alg);
  let hashes: string[] = ["sha256", "sha384", "sha512"];
  let hs: sha2.Hash[] = [sha2.Sha256, sha2.Sha384, sha2.Sha512];
  let i: i32 = 0;
  while (i < 3) {
    if (name == "rsa_pkcs1_" + hashes[i]) {
      return Ok(key.verify_pkcs1v15(hs[i], msg, sig));
    }
    if (name == "rsa_pss_" + hashes[i]) {
      return Ok(key.verify_pss(hs[i], msg, sig));
    }
    i = i + 1;
  }
  return Err(UnsupportedAlgorithm(name + " under an RSA key"));
}

function __scheme_curve(key: x509.PublicKey, scheme: i32): boolean {
  match (key) {
    x509.EcKey(curve, _) => {
      match (curve) {
        ecdsa.P256 => {
          return scheme == 0x0403;
        },
        ecdsa.P384 => {
          return scheme == 0x0503;
        }
      }
    },
    _ => {
      return false;
    }
  }
  return false;
}

function __ecdsa(curve: ecdsa.Curve, point: u8[], alg: x509.Algorithm, msg: [u8], sig: [u8]): Result[boolean, VerifyError] {
  match (ecdsa.public_key(curve, point)) {
    Ok(k) => {
      let name: string = x509.algorithm_name(alg);
      let hashes: string[] = ["sha256", "sha384", "sha512"];
      let hs: sha2.Hash[] = [sha2.Sha256, sha2.Sha384, sha2.Sha512];
      let i: i32 = 0;
      while (i < 3) {
        if (name == "ecdsa_" + hashes[i]) {
          return Ok(k.verify(hs[i], msg, sig));
        }
        i = i + 1;
      }
      return Err(UnsupportedAlgorithm(name + " under an EC key"));
    },
    Err(e) => {
      return Err(UnsupportedAlgorithm("EC key: " + e.message()));
    }
  }
  return Err(UnsupportedAlgorithm("EC key"));
}
```

### `14 pub function scheme_fits(cert: x509.Certificate, scheme: i32): boolean`

```fern
<<14 pub function scheme_fits(cert: x509.Certificate, scheme: i32): boolean>>=
pub function scheme_fits(cert: x509.Certificate, scheme: i32): boolean {
  match (cert.key) {
    x509.EcKey(curve, _) => {
      return __scheme_curve(cert.key, scheme);
    },
    x509.Ed25519Key(_) => {
      return scheme == 0x0807;
    },
    x509.RsaKey(_, _) => {
      return scheme == 0x0804 || scheme == 0x0805 || scheme == 0x0806;
    },
    x509.OtherKey(_) => {
      return false;
    }
  }
  return false;
}

pub function verify_signed(cert: x509.Certificate, scheme: i32, signed: [u8], sig: [u8]): boolean {
  if (!scheme_fits(cert, scheme)) {
    return false;
  }
  let alg: x509.Algorithm = x509.OtherAlgorithm("");
  if (scheme == 0x0403 || scheme == 0x0503) {
    alg = x509.EcdsaSha256;
    if (scheme == 0x0503) {
      alg = x509.EcdsaSha384;
    }
  } else if (scheme == 0x0804) {
    alg = x509.RsaPssSha256;
  } else if (scheme == 0x0805) {
    alg = x509.RsaPssSha384;
  } else if (scheme == 0x0806) {
    alg = x509.RsaPssSha512;
  } else if (scheme == 0x0807) {
    alg = x509.Ed25519;
  }
  match (signature_ok(cert.key, alg, signed, sig)) {
    Ok(ok) => {
      return ok;
    },
    Err(e) => {
      return false;
    }
  }
  return false;
}
```

### `15 function __usable(c: x509.Certificate, at: i32, now: i64): Result[boolean, VerifyError]`

```fern
<<15 function __usable(c: x509.Certificate, at: i32, now: i64): Result[boolean, VerifyError]>>=
function __usable(c: x509.Certificate, at: i32, now: i64): Result[boolean, VerifyError] {
  if (now < c.not_before) {
    return Err(NotYetValid(at));
  }
  if (now > c.not_after) {
    return Err(Expired(at));
  }
  if (c.unknown_critical.len() > 0) {
    return Err(UnknownCritical(at, c.unknown_critical[0]));
  }
  return Ok(true);
}

function __issuer_ok(c: x509.Certificate, at: i32, now: i64): Result[boolean, VerifyError] {
  __usable(c, at, now)?;
  if (!c.ca) {
    return Err(NotACa(at));
  }
  if (c.key_usage >= 0 && (c.key_usage & x509.KU_KEY_CERT_SIGN) == 0) {
    return Err(KeyUsage(at));
  }
  if (c.path_len >= 0 && at - 1 > c.path_len) {
    return Err(PathTooLong(at));
  }
  return Ok(true);
}
```

### `16 struct Search`

```fern
<<16 struct Search>>=
struct Search { found: boolean, err: VerifyError }
```

### `17 function __extend(below: x509.Certificate[], at: i32, inter: x509.Certificate[], roots: Roots, now: i64, se...`

```fern
<<17 function __extend(below: x509.Certificate[], at: i32, inter: x509.Certificate[], roots: Roots, now: i64, se...>>=
function __extend(below: x509.Certificate[], at: i32, inter: x509.Certificate[], roots: Roots, now: i64, server: boolean, s: Search): Search {
  if (at >= MAX_PATH) {
    return Search { found: false, err: PathTooLong(at) };
  }
  let i: i32 = 0;
  while (i < roots.certs.len()) {
    let r: Search = __try(below, roots.certs[i], at, now, s);
    if (r.found && !(server && __distrusted(roots, i, below[0]))) {
      return r;
    }
    s = Search { found: false, err: r.err };
    if (r.found) {
      s = Search { found: false, err: Distrusted(at) };
    }
    i = i + 1;
  }
  i = 0;
  while (i < inter.len()) {
    let r: Search = __try(below, inter[i], at, now, s);
    if (r.found) {
      let up: Search = __extend(below.append(inter[i]), at + 1, inter, roots, now, server, Search { found: false, err: r.err });
      if (up.found) {
        return up;
      }
      r = up;
    }
    s = Search { found: false, err: r.err };
    i = i + 1;
  }
  return s;
}

function __distrusted(roots: Roots, i: i32, leaf: x509.Certificate): boolean {
  let after: i64 = roots.distrust_after[i];
  return after > 0 as i64 && leaf.not_before > after;
}

function __try(below: x509.Certificate[], candidate: x509.Certificate, at: i32, now: i64, s: Search): Search {
  let c: x509.Certificate = below[below.len() - 1];
  if (!__same_bytes(candidate.subject.raw, c.issuer.raw)) {
    return s;
  }
  if (denied_key(candidate)) {
    return Search { found: false, err: DeniedKey(at) };
  }
  match (__issuer_ok(candidate, at, now)) {
    Ok(ok) => {
      if (!__constraints_ok(candidate, below)) {
        return Search { found: false, err: NameConstraint(at) };
      }
    },
    Err(e) => {
      return Search { found: false, err: e };
    }
  }
  match (__signed_by(c, candidate)) {
    Ok(ok) => {
      if (ok) {
        return Search { found: true, err: s.err };
      }
      return Search { found: false, err: BadSignature(at - 1) };
    },
    Err(e) => {
      return Search { found: false, err: e };
    }
  }
  return s;
}

function __constraints_ok(ca: x509.Certificate, below: x509.Certificate[]): boolean {
  for b in below {
    for d in b.dns_names {
      if (ca.permitted_dns.len() > 0 && !__dns_within_any(d, ca.permitted_dns) || __dns_within_any(d, ca.excluded_dns)) {
        return false;
      }
    }
    for ip in b.ip_addresses {
      if (ca.permitted_ip.len() > 0 && !__ip_within_any(ip, ca.permitted_ip) || __ip_within_any(ip, ca.excluded_ip)) {
        return false;
      }
    }
  }
  return true;
}

function __dns_within_any(name: string, subtrees: string[]): boolean {
  let n: string = __normal(name);
  for t in subtrees {
    let c: string = __normal(t);
    if (c.len() == 0 || n == c && !c.starts_with(".") || n.ends_with(c) && c.starts_with(".") || n.ends_with("." + c)) {
      return true;
    }
  }
  return false;
}

function __ip_within_any(ip: u8[], subtrees: u8[][]): boolean {
  for t in subtrees {
    if (t.len() == 2 * ip.len() && __masked_equal(ip, t)) {
      return true;
    }
  }
  return false;
}

function __masked_equal(ip: u8[], t: u8[]): boolean {
  let n: i32 = ip.len();
  let i: i32 = 0;
  while (i < n) {
    if ((ip[i] & t[n + i]) != (t[i] & t[n + i])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __same_bytes(a: u8[], b: u8[]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```

### `18 function __leaf_ok(leaf: x509.Certificate, name: string, now: i64): Result[boolean, VerifyError]`

```fern
<<18 function __leaf_ok(leaf: x509.Certificate, name: string, now: i64): Result[boolean, VerifyError]>>=
function __leaf_ok(leaf: x509.Certificate, name: string, now: i64): Result[boolean, VerifyError] {
  __signing_leaf(leaf, now)?;
  if (leaf.has_ext_key_usage && !__has(leaf.ext_key_usage, x509.EKU_SERVER_AUTH) && !__has(leaf.ext_key_usage, x509.EKU_ANY)) {
    return Err(NotForServers);
  }
  if (!matches_name(leaf, name)) {
    return Err(NameMismatch(name));
  }
  return Ok(true);
}

function __signing_leaf(leaf: x509.Certificate, now: i64): Result[boolean, VerifyError] {
  __usable(leaf, 0, now)?;
  if (leaf.key_usage >= 0 && (leaf.key_usage & x509.KU_DIGITAL_SIGNATURE) == 0) {
    return Err(KeyUsage(0));
  }
  return Ok(true);
}

function __has(v: string[], x: string): boolean {
  for s in v {
    if (s == x) {
      return true;
    }
  }
  return false;
}
```

### `19 pub function verify_server(chain: u8[][], roots: Roots, name: string, now: i64): Result[x509.Certificate, V...`

```fern
<<19 pub function verify_server(chain: u8[][], roots: Roots, name: string, now: i64): Result[x509.Certificate, V...>>=
pub function verify_server(chain: u8[][], roots: Roots, name: string, now: i64): Result[x509.Certificate, VerifyError] {
  let certs: x509.Certificate[] = __parsed(chain)?;
  __leaf_ok(certs[0], name, now)?;
  return __rooted(certs, roots, now, true);
}

pub function verify_client(chain: u8[][], roots: Roots, now: i64): Result[x509.Certificate, VerifyError] {
  let certs: x509.Certificate[] = __parsed(chain)?;
  __signing_leaf(certs[0], now)?;
  if (certs[0].has_ext_key_usage && !__has(certs[0].ext_key_usage, x509.EKU_CLIENT_AUTH) && !__has(certs[0].ext_key_usage, x509.EKU_ANY)) {
    return Err(NotForClients);
  }
  return __rooted(certs, roots, now, false);
}
```

### `20 function __parsed(chain: u8[][]): Result[x509.Certificate[], VerifyError]`

```fern
<<20 function __parsed(chain: u8[][]): Result[x509.Certificate[], VerifyError]>>=
function __parsed(chain: u8[][]): Result[x509.Certificate[], VerifyError] {
  if (chain.len() == 0) {
    return Err(EmptyChain);
  }
  let certs: x509.Certificate[] = [];
  let i: i32 = 0;
  while (i < chain.len()) {
    match (x509.parse(chain[i])) {
      Ok(c) => {
        certs = certs.append(c);
      },
      Err(e) => {
        return Err(Parse(i, e));
      }
    }
    i = i + 1;
  }
  if (denied_key(certs[0])) {
    return Err(DeniedKey(0));
  }
  return Ok(certs);
}
```

### `21 pub function denied_key(c: x509.Certificate): boolean`

```fern
<<21 pub function denied_key(c: x509.Certificate): boolean>>=
pub function denied_key(c: x509.Certificate): boolean {
  let h: string = hex.encode(crypto.sha256_new().update(c.spki).finish());
  for d in DENIED_SPKI {
    if (h == d) {
      return true;
    }
  }
  return false;
}
```

### `22 function __rooted(certs: x509.Certificate[], roots: Roots, now: i64, server: boolean): Result[x509.Certific...`

```fern
<<22 function __rooted(certs: x509.Certificate[], roots: Roots, now: i64, server: boolean): Result[x509.Certific...>>=
function __rooted(certs: x509.Certificate[], roots: Roots, now: i64, server: boolean): Result[x509.Certificate, VerifyError] {
  let leaf: x509.Certificate = certs[0];
  let s: Search = __extend([leaf], 1, certs.drop(1), roots, now, server, Search { found: false, err: UnknownIssuer });
  if (!s.found) {
    return Err(s.err);
  }
  return Ok(leaf);
}
```

### `23 pub function matches_name(c: x509.Certificate, name: string): boolean`

```fern
<<23 pub function matches_name(c: x509.Certificate, name: string): boolean>>=
pub function matches_name(c: x509.Certificate, name: string): boolean {
  match (net.ip_parse(name)) {
    Some(ip) => {
      let want: u8[] = ip.bytes();
      for got in c.ip_addresses {
        if (__same_bytes(got, want)) {
          return true;
        }
      }
      return false;
    },
    None => {
      if (name.contains("*")) {
        return false;
      }
      return __dns_match(c.dns_names, __normal(name));
    }
  }
  return false;
}
```

### `24 function __normal(name: string): string`

```fern
<<24 function __normal(name: string): string>>=
function __normal(name: string): string {
  let n: string = name.to_ascii_lower();
  match (n.strip_suffix(".")) {
    Some(s) => {
      return s;
    },
    None => {
      return n;
    }
  }
  return n;
}

function __dns_match(patterns: string[], name: string): boolean {
  for p in patterns {
    let pat: string = __normal(p);
    if (pat == name) {
      return true;
    }
    if let Some(rest) = pat.strip_prefix("*.") {
      if (__wild(rest, name)) {
        return true;
      }
    }
  }
  return false;
}

function __wild(rest: string, name: string): boolean {
  match (name.strip_suffix("." + rest)) {
    Some(label) => {
      return label.len() > 0 && !label.contains(".") && rest.contains(".");
    },
    None => {
      return false;
    }
  }
  return false;
}
```
