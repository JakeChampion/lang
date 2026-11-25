# Android key attestation

An Android device can attest a key that it keeps in Android Keystore. The attestation is a certificate chain. The leaf certificate holds the public key and a key description extension (OID 1.3.6.1.4.1.11129.2.1.17). The secure hardware of the device signs the leaf, and the chain goes up to a Google attestation root.

This module reads the key description and checks the chain. It does not call Java, so a server, a peer device or a test can use it.

## Model

`parse(ext)` reads the value of a key description extension. `description(cert)` finds the extension in a parsed certificate and reads it.

A `KeyDescription` holds these fields:

- the attestation version and the KeyMint (or Keymaster) version
- the security level of the attestation and of KeyMint: software, a trusted execution environment, or StrongBox
- the attestation challenge and the unique ID
- the software-enforced and the hardware-enforced authorization lists

An `Authorizations` value keeps each entry of a list as its KeyMint tag and the DER of its value. The `TAG_*` constants name the tags that this module documents. `integer`, `integers`, `octets` and `has` read an entry by its tag. The parser reads the root of trust (tag 704) to a `RootOfTrust`.

`verify(chain, challenge, now)` checks a chain from the leaf to the root and gives an `Attestation`: the key description, the attested public key, and the security level of the attestation. `verify_with` does the same check with other trusted root keys.

## Contracts

`verify` accepts a chain only when all these conditions are true:

- The chain has at least two certificates, and each one parses.
- No certificate has a critical extension that the parser does not know.
- Each certificate is the issuer of the certificate before it: the names agree and the signature is correct.
- Each issuer is a certificate authority. When it has a key usage extension, the extension permits certificate signatures.
- Each certificate other than the root is valid at `now`.
- The SHA-256 of the root's subject public key is one of the trusted root keys.
- The leaf has a key description, and its challenge is equal to `challenge`.

The root is a trust anchor, so its validity dates are not checked. Google issued the attestation root key again in 2022 with new dates. Chains from devices can still hold the certificate of 2016, which expired on 2026-05-24.

The security level is a property of the key description. A chain to a Google root with the software security level does not prove that the key is in hardware. The caller decides which levels to accept.

`verify` does not check revocation. Google publishes the revocation status of attestation certificates at `https://android.googleapis.com/attestation/status`. A server that accepts attestations must check each serial number of the chain against that list.

An authorization list must not have the same tag two times. The parser refuses such a list, because the tag then has two values and no rule selects one.

```fern
<<*>>=
<<introduction>>

<<dependencies>>

<<tags>>

<<types>>

<<errors>>

<<integers>>

<<authorization lists>>

<<root of trust>>

<<key description>>

<<chain>>
```

### Introduction

```fern
<<introduction>>=
// Read Android key attestation records and check their certificate chains.
```

### Dependencies

```fern
<<dependencies>>=
import "std/crypto";
import "std/hex";
import "std/time";
import "std/tls/der";
import "std/tls/verify";
import "std/tls/x509";
import "core/error";
import "core/cmp";
```

### Tags

The tag numbers come from the KeyMint `Tag` definition. A tag number here is the number without the type bits of KeyMint.

```fern
<<tags>>=
pub const KEY_DESCRIPTION: string = "1.3.6.1.4.1.11129.2.1.17";

// The SHA-256 of the subject public keys of the Google attestation roots: the RSA-4096 key and the P-384 key of Key Attestation CA1.
pub const GOOGLE_ROOTS: string[] = ["feb2ea7551ee316ed4bb443c8293b884dbfdea40b603ee3e4f4a897e4580fbae", "3ee44512a1af2beb39c889490c60ea3f82e43f5d5a5532f5ab9419f676cd07ec"];

pub const TAG_PURPOSE: i32 = 1;

pub const TAG_ALGORITHM: i32 = 2;

pub const TAG_KEY_SIZE: i32 = 3;

pub const TAG_DIGEST: i32 = 5;

pub const TAG_PADDING: i32 = 6;

pub const TAG_EC_CURVE: i32 = 10;

pub const TAG_RSA_PUBLIC_EXPONENT: i32 = 200;

pub const TAG_NO_AUTH_REQUIRED: i32 = 503;

pub const TAG_CREATION_DATE_TIME: i32 = 701;

pub const TAG_ORIGIN: i32 = 702;

pub const TAG_ROOT_OF_TRUST: i32 = 704;

pub const TAG_OS_VERSION: i32 = 705;

pub const TAG_OS_PATCH_LEVEL: i32 = 706;

pub const TAG_ATTESTATION_APPLICATION_ID: i32 = 709;

pub const TAG_VENDOR_PATCH_LEVEL: i32 = 718;

pub const TAG_BOOT_PATCH_LEVEL: i32 = 719;
```

### Types

```fern
<<types>>=
pub enum SecurityLevel { Software, TrustedEnvironment, StrongBox }

pub enum BootState { Verified, SelfSigned, Unverified, Failed }

pub struct RootOfTrust { boot_key: u8[], locked: boolean, state: BootState, boot_hash: u8[] }

// One entry of an authorization list: its KeyMint tag number and the DER of its value.
pub struct Entry { tag: i32, value: u8[] }

pub struct Authorizations { entries: Entry[], root_of_trust: Option[RootOfTrust] }

pub struct KeyDescription {
  attestation_version: i32,
  attestation_level: SecurityLevel,
  keymint_version: i32,
  keymint_level: SecurityLevel,
  challenge: u8[],
  unique_id: u8[],
  software: Authorizations,
  hardware: Authorizations,
}

pub struct Attestation { description: KeyDescription, key: x509.PublicKey, level: SecurityLevel }

pub function level_name(l: SecurityLevel): string {
  match (l) {
    Software => {
      return "software";
    },
    TrustedEnvironment => {
      return "trusted environment";
    },
    StrongBox => {
      return "StrongBox";
    }
  }
  return "";
}

pub function boot_state_name(s: BootState): string {
  match (s) {
    Verified => {
      return "verified";
    },
    SelfSigned => {
      return "self-signed";
    },
    Unverified => {
      return "unverified";
    },
    Failed => {
      return "failed";
    }
  }
  return "";
}
```

### Errors

```fern
<<errors>>=
pub enum AttestationError {
  Malformed(string),
  NoDescription,
  BadCertificate(i32, string),
  NotSigned(i32),
  OutOfDate(i32),
  UntrustedRoot,
  WrongChallenge,
}

pub function (e: AttestationError) to_string(): string {
  return e.message();
}

impl error.Error for AttestationError {}

impl cmp.Display for AttestationError {}

pub function (e: AttestationError) message(): string {
  match (e) {
    Malformed(what) => {
      return "attestation: malformed " + what;
    },
    NoDescription => {
      return "attestation: the leaf has no key description";
    },
    BadCertificate(i, why) => {
      return "attestation: certificate " + i.to_string() + ": " + why;
    },
    NotSigned(i) => {
      return "attestation: certificate " + i.to_string() + " is not signed by the next certificate";
    },
    OutOfDate(i) => {
      return "attestation: certificate " + i.to_string() + " is not valid now";
    },
    UntrustedRoot => {
      return "attestation: the root key is not trusted";
    },
    WrongChallenge => {
      return "attestation: the challenge is not the expected value";
    }
  }
  return "";
}

function __d[T](r: Result[T, der.DerError]): Result[T, AttestationError] {
  match (r) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(Malformed(e.message()));
    }
  }
  return Err(Malformed("DER"));
}
```

### Integers

KeyMint writes dates in milliseconds and patch levels as decimal dates, so an integer can need 64 bits.

```fern
<<integers>>=
function __int64(b: [u8], e: der.Element): Result[i64, AttestationError] {
  let v = __d(der.unsigned(b, e))?;
  if (v.len() > 8 || v.len() == 8 && v[0] >= 0x80) {
    return Err(Malformed("integer"));
  }
  let out = 0 as i64;
  let i = 0;
  while (i < v.len()) {
    out = out << 8 | v[i] as i64;
    i += 1;
  }
  return Ok(out);
}

function __level(b: [u8], e: der.Element): Result[SecurityLevel, AttestationError] {
  let n = __d(der.enumerated(b, e))?;
  if (n == 0) {
    return Ok(Software);
  }
  if (n == 1) {
    return Ok(TrustedEnvironment);
  }
  if (n == 2) {
    return Ok(StrongBox);
  }
  return Err(Malformed("security level " + n.to_string()));
}
```

### Authorization lists

Each entry is an explicit context tag with one value inside it.

```fern
<<authorization lists>>=
function __tag_number(tag: i32): i32 {
  if ((tag & 0x1f) == 0x1f) {
    return tag >> 8;
  }
  return tag & 0x1f;
}

function __authorizations(b: [u8], e: der.Element): Result[Authorizations, AttestationError] {
  if (e.tag != der.SEQUENCE) {
    return Err(Malformed("authorization list"));
  }
  let parts = __d(der.children(b, e))?;
  let entries: Entry[] = [];
  let rot: Option[RootOfTrust] = None;
  for p in parts {
    let entry = __entry(b, p, entries)?;
    if (entry.tag == TAG_ROOT_OF_TRUST) {
      rot = Some(__root_of_trust(entry.value, __d(der.read(entry.value, 0))?)?);
    }
    entries = entries.append(entry);
  }
  return Ok(Authorizations { entries: entries, root_of_trust: rot });
}

function __entry(b: [u8], p: der.Element, before: Entry[]): Result[Entry, AttestationError] {
  let tag = __tag_number(p.tag);
  if ((p.tag & 0xe0) != 0xa0) {
    return Err(Malformed("authorization tag " + p.tag.to_string()));
  }
  let inner = __d(der.read(b, p.from))?;
  if (inner.next != p.to) {
    return Err(Malformed("authorization " + tag.to_string()));
  }
  for seen in before {
    if (seen.tag == tag) {
      return Err(Malformed("repeated authorization " + tag.to_string()));
    }
  }
  return Ok(Entry { tag: tag, value: der.whole(b, inner) });
}

function (a: Authorizations) __value(tag: i32): Option[u8[]] {
  for e in a.entries {
    if (e.tag == tag) {
      return Some(e.value);
    }
  }
  return None;
}

// True when the list has the tag. Use it for the tags without a value, such as TAG_NO_AUTH_REQUIRED.
pub function (a: Authorizations) has(tag: i32): boolean {
  match (a.__value(tag)) {
    Some(_) => {
      return true;
    },
    None => {
      return false;
    }
  }
  return false;
}

// The INTEGER value of the tag, or None when the list has no such INTEGER.
pub function (a: Authorizations) integer(tag: i32): Option[i64] {
  if let Some(v) = a.__value(tag) {
    if let Ok(e) = der.only(v, der.INTEGER) {
      if let Ok(n) = __int64(v, e) {
        return Some(n);
      }
    }
  }
  return None;
}

// The values of a SET OF INTEGER tag, such as TAG_PURPOSE or TAG_DIGEST.
pub function (a: Authorizations) integers(tag: i32): Option[i64[]] {
  if let Some(v) = a.__value(tag) {
    if let Ok(set) = der.only(v, der.SET) {
      if let Ok(items) = der.children(v, set) {
        let out: i64[] = [];
        for item in items {
          if (item.tag != der.INTEGER) {
            return None;
          }
          match (__int64(v, item)) {
            Ok(n) => {
              out = out.append(n);
            },
            Err(_) => {
              return None;
            }
          }
        }
        return Some(out);
      }
    }
  }
  return None;
}

// The OCTET STRING value of the tag, such as TAG_ATTESTATION_APPLICATION_ID.
pub function (a: Authorizations) octets(tag: i32): Option[u8[]] {
  if let Some(v) = a.__value(tag) {
    if let Ok(e) = der.only(v, der.OCTET_STRING) {
      return Some(der.bytes(v, e));
    }
  }
  return None;
}
```

### Root of trust

```fern
<<root of trust>>=
function __root_of_trust(b: [u8], e: der.Element): Result[RootOfTrust, AttestationError] {
  let parts = __d(der.children(b, e))?;
  if (e.tag != der.SEQUENCE || parts.len() < 3 || parts.len() > 4 || parts[0].tag != der.OCTET_STRING) {
    return Err(Malformed("root of trust"));
  }
  return Ok(RootOfTrust { boot_key: der.bytes(b, parts[0]), locked: __d(der.boolean_value(b, parts[1]))?, state: __boot_state(b, parts[2])?, boot_hash: __boot_hash(b, parts)? });
}

function __boot_state(b: [u8], e: der.Element): Result[BootState, AttestationError] {
  let n = __d(der.enumerated(b, e))?;
  let states = [Verified, SelfSigned, Unverified, Failed];
  if (n >= states.len()) {
    return Err(Malformed("verified boot state " + n.to_string()));
  }
  return Ok(states[n]);
}

// The verified boot hash is in attestation version 3 and later.
function __boot_hash(b: [u8], parts: der.Element[]): Result[u8[], AttestationError] {
  if (parts.len() < 4) {
    return Ok([]);
  }
  if (parts[3].tag != der.OCTET_STRING) {
    return Err(Malformed("verified boot hash"));
  }
  return Ok(der.bytes(b, parts[3]));
}
```

### Key description

```fern
<<key description>>=
// Reads the value of a key description extension.
pub function parse(ext: [u8]): Result[KeyDescription, AttestationError] {
  let p = __fields(ext)?;
  let head = __head(ext, p)?;
  return Ok(KeyDescription { ...head, software: __authorizations(ext, p[6])?, hardware: __authorizations(ext, p[7])? });
}

function __fields(ext: [u8]): Result[der.Element[], AttestationError] {
  let p = __d(der.children(ext, __d(der.only(ext, der.SEQUENCE))?))?;
  let want = [der.INTEGER, der.ENUMERATED, der.INTEGER, der.ENUMERATED, der.OCTET_STRING, der.OCTET_STRING, der.SEQUENCE, der.SEQUENCE];
  if (p.len() != want.len()) {
    return Err(Malformed("key description"));
  }
  let i = 0;
  while (i < p.len()) {
    if (p[i].tag != want[i]) {
      return Err(Malformed("key description"));
    }
    i += 1;
  }
  return Ok(p);
}

function __head(ext: [u8], p: der.Element[]): Result[KeyDescription, AttestationError] {
  let none = Authorizations { entries: [], root_of_trust: None };
  return Ok(KeyDescription { attestation_version: __d(der.small(ext, p[0]))?, attestation_level: __level(ext, p[1])?, keymint_version: __d(der.small(ext, p[2]))?, keymint_level: __level(ext, p[3])?, challenge: der.bytes(ext, p[4]), unique_id: der.bytes(ext, p[5]), software: none, hardware: none });
}

// Finds the key description extension of a certificate and reads it.
pub function description(c: x509.Certificate): Result[KeyDescription, AttestationError] {
  match (x509.extension(c, KEY_DESCRIPTION)) {
    Some(ext) => {
      return parse(ext.value);
    },
    None => {
      return Err(NoDescription);
    }
  }
  return Err(NoDescription);
}
```

### Chain

The chain starts at the leaf. Each certificate after it is the issuer of the one before it.

```fern
<<chain>>=
// Checks an attestation chain to a Google attestation root.
pub function verify(chain: u8[][], challenge: [u8], now: time.Timestamp): Result[Attestation, AttestationError] {
  return verify_with(chain, GOOGLE_ROOTS, challenge, now);
}

// Checks an attestation chain to a root whose public key has one of these SHA-256 values, in lowercase hex.
pub function verify_with(chain: u8[][], roots: string[], challenge: [u8], now: time.Timestamp): Result[Attestation, AttestationError] {
  let certs = __parsed(chain)?;
  __linked(certs, now)?;
  if (!__trusted(certs[certs.len() - 1], roots)) {
    return Err(UntrustedRoot);
  }
  let d = description(certs[0])?;
  if (!__same(d.challenge, challenge)) {
    return Err(WrongChallenge);
  }
  return Ok(Attestation { description: d, key: certs[0].key, level: d.attestation_level });
}

function __parsed(chain: u8[][]): Result[x509.Certificate[], AttestationError] {
  if (chain.len() < 2) {
    return Err(Malformed("chain of fewer than two certificates"));
  }
  let certs: x509.Certificate[] = [];
  let i = 0;
  while (i < chain.len()) {
    match (x509.parse(chain[i])) {
      Ok(c) => {
        certs = certs.append(__usable(c, i)?);
      },
      Err(e) => {
        return Err(BadCertificate(i, e.message()));
      }
    }
    i += 1;
  }
  return Ok(certs);
}

// The root is a trust anchor, so its dates are not checked.
function __linked(certs: x509.Certificate[], now: time.Timestamp): Result[boolean, AttestationError] {
  let i = 0;
  while (i < certs.len() - 1) {
    __issued(certs[i], certs[i + 1], i)?;
    if (now.sec < certs[i].not_before || now.sec > certs[i].not_after) {
      return Err(OutOfDate(i));
    }
    i += 1;
  }
  return Ok(true);
}

function __usable(c: x509.Certificate, i: i32): Result[x509.Certificate, AttestationError] {
  if (c.unknown_critical.len() > 0) {
    return Err(BadCertificate(i, "unknown critical extension " + c.unknown_critical[0]));
  }
  return Ok(c);
}

function __issued(c: x509.Certificate, issuer: x509.Certificate, i: i32): Result[boolean, AttestationError] {
  if (!issuer.ca || issuer.key_usage != -1 && (issuer.key_usage & x509.KU_KEY_CERT_SIGN) == 0) {
    return Err(BadCertificate(i + 1, "not a certificate authority"));
  }
  if (!__same(c.issuer.raw, issuer.subject.raw)) {
    return Err(NotSigned(i));
  }
  match (verify.signature_ok(issuer.key, c.algorithm, c.tbs, c.signature)) {
    Ok(good) => {
      if (!good) {
        return Err(NotSigned(i));
      }
    },
    Err(e) => {
      return Err(BadCertificate(i, e.message()));
    }
  }
  return Ok(true);
}

function __trusted(root: x509.Certificate, roots: string[]): boolean {
  let h = hex.encode(crypto.sha256_new().update(root.spki).finish());
  for r in roots {
    if (r == h) {
      return true;
    }
  }
  return false;
}

function __same(a: [u8], b: [u8]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i += 1;
  }
  return true;
}
```
