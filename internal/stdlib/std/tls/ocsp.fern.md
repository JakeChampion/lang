# OCSP responses

This module reads OCSP responses (RFC 6960) and checks the status that one gives for a certificate.

## Model

An OCSP response is a status for one or more certificates, signed by a responder. A TLS server can staple a response to its certificate, so the client does not ask the responder itself. `status` finds the response for a leaf certificate, checks the signature of the responder and the validity window, and gives the status: `Good`, `Revoked` or `Unknown`.

## Contracts

The responder is the issuer of the leaf, or a responder that the issuer delegated. A delegated responder has a certificate that the issuer signed, with the `id-kp-OCSPSigning` extended key usage, valid at the given time. The response names its responder by name or by key, and `status` uses only a certificate with that name or key.

The CertID of the response can use SHA-1 or SHA-256. A response is valid from its `thisUpdate` time until its `nextUpdate` time. A response with no `nextUpdate` is valid for `MAX_AGE` seconds after its `thisUpdate`.

`status` does not check whether the responder certificate is revoked, and it does not read a nonce. A stapled response has no nonce.

## Executable example

```fern test name=std-tls-ocsp-example
import "std/tls/ocsp";

function main(): i32 {
  // An OCSP response with the status internalError and no body.
  let refused: u8[] = [0x30 as u8, 0x03 as u8, 0x0a as u8, 0x01 as u8, 0x02 as u8];
  match (ocsp.parse(refused)) {
    Err(ocsp.NotSuccessful(code)) => {
      if (code != 2) { return 1; }
    },
    _ => {
      return 2;
    }
  }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<types>>

<<errors>>

<<parse>>

<<status>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads OCSP responses and checks the status of a certificate.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/crypto";
import "std/tls/der";
import "std/tls/verify";
import "std/tls/x509";
import "std/time";
import "core/error";
import "core/cmp";
```

### `types`

`Revoked` holds the revocation time in seconds since the Unix epoch. A `SingleResponse` with no `nextUpdate` has `next_update` set to -1.

```fern
<<types>>=
pub enum CertStatus { Good, Revoked(i64), Unknown }

pub struct SingleResponse { hash: string, name_hash: u8[], key_hash: u8[], serial: u8[], status: CertStatus, this_update: i64, next_update: i64 }

// The responder has a name or a key hash: the other field is empty.
pub struct Response { tbs: u8[], responder_name: u8[], responder_key_hash: u8[], produced_at: i64, responses: SingleResponse[], algorithm: x509.Algorithm, signature: u8[], certs: x509.Certificate[] }

// The validity of a response with no nextUpdate, in seconds.
pub const MAX_AGE: i64 = 86400;

const BASIC_RESPONSE: string = "1.3.6.1.5.5.7.48.1.1";

const SHA1: string = "1.3.14.3.2.26";

const SHA256: string = "2.16.840.1.101.3.4.2.1";

pub const EKU_OCSP_SIGNING: string = "1.3.6.1.5.5.7.3.9";

const ENUMERATED: i32 = 0x0a;
```

### `errors`

```fern
<<errors>>=
pub enum OcspError {
  Der(der.DerError),
  X509(x509.X509Error),
  Malformed(string),
  NotSuccessful(i32),
  UnsupportedType(string),
  UnsupportedHash(string),
  UnauthorizedResponder,
  BadSignature,
  NoMatchingResponse,
  NotYetValid(i64),
  Expired(i64),
}

pub function (e: OcspError) to_string(): string {
  return e.message();
}

impl error.Error for OcspError {}

impl cmp.Display for OcspError {}

// fern-lint: allow cyclomatic-complexity
pub function (e: OcspError) message(): string {
  match (e) {
    Der(d) => {
      return "ocsp: " + d.message();
    },
    X509(x) => {
      return "ocsp: " + x.message();
    },
    Malformed(what) => {
      return "ocsp: malformed " + what;
    },
    NotSuccessful(code) => {
      return "ocsp: the responder gave the status " + code.to_string() + " and no response";
    },
    UnsupportedType(oid) => {
      return "ocsp: unsupported response type " + oid;
    },
    UnsupportedHash(oid) => {
      return "ocsp: unsupported CertID hash " + oid;
    },
    UnauthorizedResponder => {
      return "ocsp: the responder is not the issuer or a responder that the issuer delegated";
    },
    BadSignature => {
      return "ocsp: the signature of the response does not verify";
    },
    NoMatchingResponse => {
      return "ocsp: the response has no status for the certificate";
    },
    NotYetValid(t) => {
      return "ocsp: the response is not valid before " + t.to_string();
    },
    Expired(t) => {
      return "ocsp: the response expired at " + t.to_string();
    }
  }
  return "";
}

function __d[T](r: Result[T, der.DerError]): Result[T, OcspError] {
  match (r) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(Der(e));
    }
  }
  return Err(Malformed("der"));
}

function __tag(e: der.Element, tag: i32, what: string): Result[boolean, OcspError] {
  if (e.tag != tag) {
    return Err(Malformed(what));
  }
  return Ok(true);
}

// The single element, with the tag `tag`, inside `e`, an explicit tag
// `outer`.
function __explicit(b: [u8], e: der.Element, outer: i32, tag: i32, what: string): Result[der.Element, OcspError] {
  if (e.tag != outer) {
    return Err(Malformed(what));
  }
  let inner = __d(der.read(b, e.from))?;
  if (inner.tag != tag || inner.next != e.to) {
    return Err(Malformed(what));
  }
  return Ok(inner);
}

// The elements of `e`, a SEQUENCE with from `min` to `max` elements.
function __seq(b: [u8], e: der.Element, min: i32, max: i32, what: string): Result[der.Element[], OcspError] {
  if (e.tag != der.SEQUENCE) {
    return Err(Malformed(what));
  }
  let p = __d(der.children(b, e))?;
  if (p.len() < min || p.len() > max) {
    return Err(Malformed(what));
  }
  return Ok(p);
}

function __octets(b: [u8], e: der.Element, what: string): Result[u8[], OcspError] {
  __tag(e, der.OCTET_STRING, what)?;
  return Ok(der.bytes(b, e));
}
```

### `parse`

`parse` reads an OCSPResponse. A response whose `responseStatus` is not `successful` is the error `NotSuccessful`, with the status code.

```fern
<<parse>>=
pub function parse(raw: [u8]): Result[Response, OcspError] {
  let parts = __seq(raw, __d(der.only(raw, der.SEQUENCE))?, 1, 2, "OCSPResponse")?;
  let code = __status_code(raw, parts[0])?;
  if (code != 0) {
    return Err(NotSuccessful(code));
  }
  if (parts.len() != 2) {
    return Err(Malformed("responseBytes"));
  }
  let rb = __explicit(raw, parts[1], der.context(0, true), der.SEQUENCE, "responseBytes")?;
  return __basic(__response_bytes(raw, rb)?);
}

function __status_code(b: [u8], e: der.Element): Result[i32, OcspError] {
  if (e.tag != ENUMERATED || e.to - e.from != 1) {
    return Err(Malformed("responseStatus"));
  }
  return Ok(b[e.from] as i32);
}

function __response_bytes(b: [u8], e: der.Element): Result[u8[], OcspError] {
  let p = __seq(b, e, 2, 2, "ResponseBytes")?;
  let kind = __d(der.oid(b, p[0]))?;
  if (kind != BASIC_RESPONSE) {
    return Err(UnsupportedType(kind));
  }
  return __octets(b, p[1], "response");
}

function __basic(b: u8[]): Result[Response, OcspError] {
  let p = __seq(b, __d(der.only(b, der.SEQUENCE))?, 3, 4, "BasicOCSPResponse")?;
  let sig = __d(der.bit_string(b, p[2]))?;
  if (sig.unused != 0) {
    return Err(Malformed("signature"));
  }
  let certs: x509.Certificate[] = [];
  if (p.len() == 4) {
    certs = __certs(b, p[3])?;
  }
  let r = Response { tbs: der.whole(b, p[0]), responder_name: [], responder_key_hash: [], produced_at: 0 as i64, responses: [], algorithm: __x(x509.algorithm_identifier(b, p[1]))?, signature: sig.bytes, certs: certs };
  return __response_data(b, __seq(b, p[0], 3, 5, "ResponseData")?, r);
}

function __certs(b: [u8], e: der.Element): Result[x509.Certificate[], OcspError] {
  let list = __explicit(b, e, der.context(0, true), der.SEQUENCE, "certs")?;
  let out: x509.Certificate[] = [];
  for c in __d(der.children(b, list))? {
    out = out.append(__x(x509.parse(der.whole(b, c)))?);
  }
  return Ok(out);
}

function __x[T](r: Result[T, x509.X509Error]): Result[T, OcspError] {
  match (r) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(X509(e));
    }
  }
  return Err(Malformed("certificate"));
}

function __response_data(b: [u8], p: der.Element[], r: Response): Result[Response, OcspError] {
  let i = __after_version(b, p)?;
  if (p.len() < i + 3) {
    return Err(Malformed("ResponseData"));
  }
  let out = __responder(b, p[i], r)?;
  let singles = __singles(b, p[i + 2])?;
  let end = i + 3;
  if (end < p.len() && p[end].tag == der.context(1, true)) {
    end += 1;
  }
  if (end != p.len()) {
    return Err(Malformed("ResponseData"));
  }
  return Ok(Response { ...out, produced_at: __time(b, p[i + 1])?, responses: singles });
}

// The index after the version of a ResponseData, which must be v1.
function __after_version(b: [u8], p: der.Element[]): Result[i32, OcspError] {
  if (p.len() == 0 || p[0].tag != der.context(0, true)) {
    return Ok(0);
  }
  let v = __explicit(b, p[0], der.context(0, true), der.INTEGER, "version")?;
  if (__d(der.small(b, v))? != 0) {
    return Err(Malformed("version"));
  }
  return Ok(1);
}

function __responder(b: [u8], e: der.Element, r: Response): Result[Response, OcspError] {
  if (e.tag == der.context(1, true)) {
    return Ok(Response { ...r, responder_name: der.whole(b, __explicit(b, e, der.context(1, true), der.SEQUENCE, "responderID")?) });
  }
  let key = __explicit(b, e, der.context(2, true), der.OCTET_STRING, "responderID")?;
  return Ok(Response { ...r, responder_key_hash: der.bytes(b, key) });
}

function __singles(b: [u8], e: der.Element): Result[SingleResponse[], OcspError] {
  let out: SingleResponse[] = [];
  for s in __seq(b, e, 0, 0x7fffffff, "responses")? {
    out = out.append(__single(b, s)?);
  }
  return Ok(out);
}

function __time(b: [u8], e: der.Element): Result[i64, OcspError] {
  __tag(e, der.GENERALIZED_TIME, "time")?;
  return __d(der.time(b, e));
}

function __single(b: [u8], e: der.Element): Result[SingleResponse, OcspError] {
  let p = __seq(b, e, 3, 5, "SingleResponse")?;
  let s = SingleResponse { ...__cert_id(b, p[0])?, status: __cert_status(b, p[1])?, this_update: __time(b, p[2])? };
  return __single_tail(b, p, s);
}

// The optional nextUpdate and singleExtensions after the thisUpdate of a
// SingleResponse.
function __single_tail(b: [u8], p: der.Element[], s: SingleResponse): Result[SingleResponse, OcspError] {
  let out = s;
  let j = 3;
  if (j < p.len() && p[j].tag == der.context(0, true)) {
    out = SingleResponse { ...out, next_update: __time(b, __explicit(b, p[j], der.context(0, true), der.GENERALIZED_TIME, "nextUpdate")?)? };
    j += 1;
  }
  if (j < p.len() && p[j].tag == der.context(1, true)) {
    j += 1;
  }
  if (j != p.len()) {
    return Err(Malformed("SingleResponse"));
  }
  return Ok(out);
}

// A SingleResponse with the fields of `e`, a CertID, and no status.
function __cert_id(b: [u8], e: der.Element): Result[SingleResponse, OcspError] {
  let id = __seq(b, e, 4, 4, "CertID")?;
  let alg = __seq(b, id[0], 1, 2, "hashAlgorithm")?;
  if (alg.len() == 2 && (alg[1].tag != der.NULL || alg[1].to != alg[1].from)) {
    return Err(Malformed("hashAlgorithm"));
  }
  __tag(id[3], der.INTEGER, "serialNumber")?;
  return Ok(SingleResponse { hash: __d(der.oid(b, alg[0]))?, name_hash: __octets(b, id[1], "issuerNameHash")?, key_hash: __octets(b, id[2], "issuerKeyHash")?, serial: der.bytes(b, id[3]), status: Unknown, this_update: 0 as i64, next_update: -(1 as i64) });
}

function __cert_status(b: [u8], e: der.Element): Result[CertStatus, OcspError] {
  if (e.tag == der.context(0, false) && e.to == e.from) {
    return Ok(Good);
  }
  if (e.tag == der.context(2, false) && e.to == e.from) {
    return Ok(Unknown);
  }
  if (e.tag != der.context(1, true)) {
    return Err(Malformed("certStatus"));
  }
  let info = __d(der.children(b, e))?;
  if (info.len() == 0 || info.len() > 2) {
    return Err(Malformed("RevokedInfo"));
  }
  return Ok(Revoked(__time(b, info[0])?));
}
```

### `status`

`status` checks a response for `leaf`, whose issuer is `issuer`, at the time `now`. It gives the status only when four checks pass. The issuer must authorize the responder, and the signature of the responder must verify. The response must have a CertID for the leaf, and the time must be in the validity window of that CertID.

```fern
<<status>>=
pub function status(raw: [u8], leaf: x509.Certificate, issuer: x509.Certificate, now: time.Timestamp): Result[CertStatus, OcspError] {
  let r = parse(raw)?;
  let signer = __signer(r, issuer, now.sec)?;
  match (verify.signature_ok(signer.key, r.algorithm, r.tbs, r.signature)) {
    Ok(true) => {},
    _ => {
      return Err(BadSignature);
    }
  }
  let s = __matching(r, leaf, issuer)?;
  if (now.sec < s.this_update) {
    return Err(NotYetValid(s.this_update));
  }
  let until = s.next_update;
  if (until < 0 as i64) {
    until = s.this_update + MAX_AGE;
  }
  if (now.sec > until) {
    return Err(Expired(until));
  }
  return Ok(s.status);
}

// The certificate that signed `r`: the issuer, or a delegated responder that
// the response carries.
function __signer(r: Response, issuer: x509.Certificate, now: i64): Result[x509.Certificate, OcspError] {
  if (__names(r, issuer)) {
    return Ok(issuer);
  }
  for c in r.certs {
    if (__names(r, c) && __delegated(c, issuer, now)) {
      return Ok(c);
    }
  }
  return Err(UnauthorizedResponder);
}

function __names(r: Response, c: x509.Certificate): boolean {
  if (r.responder_name.len() > 0) {
    return __same(r.responder_name, c.subject.raw);
  }
  match (__key_bits(c)) {
    Ok(bits) => {
      return __same(r.responder_key_hash, crypto.sha1(bits));
    },
    Err(_) => {
      return false;
    }
  }
  return false;
}

function __delegated(c: x509.Certificate, issuer: x509.Certificate, now: i64): boolean {
  if (!__same(c.issuer.raw, issuer.subject.raw) || now < c.not_before || now > c.not_after) {
    return false;
  }
  if (!c.has_ext_key_usage || !__has(c.ext_key_usage, EKU_OCSP_SIGNING) || c.unknown_critical.len() != 0) {
    return false;
  }
  match (verify.signature_ok(issuer.key, c.algorithm, c.tbs, c.signature)) {
    Ok(ok) => {
      return ok;
    },
    Err(_) => {
      return false;
    }
  }
  return false;
}

function __matching(r: Response, leaf: x509.Certificate, issuer: x509.Certificate): Result[SingleResponse, OcspError] {
  if (!__same(leaf.issuer.raw, issuer.subject.raw)) {
    return Err(NoMatchingResponse);
  }
  let bits = __key_bits(issuer)?;
  for s in r.responses {
    if (__same(s.serial, leaf.serial)) {
      let hashes = __hashes(s.hash, issuer.subject.raw, bits)?;
      if (__same(s.name_hash, hashes.0) && __same(s.key_hash, hashes.1)) {
        return Ok(s);
      }
    }
  }
  return Err(NoMatchingResponse);
}

function __hashes(oid: string, name: [u8], key: [u8]): Result[(u8[], u8[]), OcspError] {
  if (oid == SHA1) {
    return Ok((crypto.sha1(name), crypto.sha1(key)));
  }
  if (oid == SHA256) {
    return Ok((crypto.sha256(name), crypto.sha256(key)));
  }
  return Err(UnsupportedHash(oid));
}

// The subjectPublicKey bits of `c`, without the BIT STRING header.
function __key_bits(c: x509.Certificate): Result[u8[], OcspError] {
  let seq = __d(der.only(c.spki, der.SEQUENCE))?;
  let p = __d(der.children(c.spki, seq))?;
  if (p.len() != 2) {
    return Err(Malformed("SubjectPublicKeyInfo"));
  }
  return Ok(__d(der.bit_string(c.spki, p[1]))?.bytes);
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

function __has(v: string[], x: string): boolean {
  for s in v {
    if (s == x) {
      return true;
    }
  }
  return false;
}
```
