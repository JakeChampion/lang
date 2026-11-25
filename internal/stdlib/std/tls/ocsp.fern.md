# OCSP responses

This module checks an OCSP response (RFC 6960) for the status of one certificate. A TLS server staples the response to its certificate, so the client does not ask the responder itself.

## Model

`check` parses a DER `OCSPResponse` of the basic type and finds the `SingleResponse` for the certificate. The `CertID` must have the serial number of the certificate, and the hashes of the name and the key of its issuer. SHA-1, SHA-256, SHA-384 and SHA-512 are the accepted hash algorithms of a `CertID`.

The response must have a signature of the issuer, or of a responder that the issuer delegated. A delegated responder has a certificate in the response. That certificate has the issuer's signature, the extended key usage `id-kp-OCSPSigning`, and a validity window that contains the given time.

A response is current from its `thisUpdate` to its `nextUpdate`. A response with no `nextUpdate` is current for one day after its `thisUpdate`.

`Policy` selects what a TLS client does with a stapled response. `NoCheck` is the default and asks for no response. `CheckStapled` asks for a response and checks it when the server staples one. `RequireStapled` refuses a server that staples no response.

## Contracts

`check` gives a `Status` only for a response with a correct signature, a correct responder, and a current window. Each other response gives an `OcspError`. `require_good` also refuses the status `Revoked` and the status `Unknown`.

```fern
<<*>>=
// OCSP responses (RFC 6960) for the status of a certificate.

import "std/crypto";
import "std/time";
import "std/result";
import "std/tls/der";
import "std/tls/verify";
import "std/tls/x509";
import "core/error";
import "core/cmp";

pub enum Policy { NoCheck, CheckStapled, RequireStapled }

// Revoked holds the revocation time in seconds since the Unix epoch.
pub enum Status { Good, Revoked(i64), Unknown }

pub enum OcspError {
  Der(der.DerError),
  Malformed(string),
  NotSuccessful(i32),
  NotBasic(string),
  UnsupportedHash(string),
  NoResponse,
  UnauthorizedResponder,
  BadSignature,
  Signature(verify.VerifyError),
  NotYetValid,
  Expired,
  CertificateRevoked(i64),
  StatusUnknown,
  NotStapled,
  NoIssuer,
}

pub function (e: OcspError) to_string(): string {
  return e.message();
}

impl error.Error for OcspError {}

impl cmp.Display for OcspError {}

pub function (e: OcspError) message(): string {
  match (e) {
    Der(d) => {
      return "the OCSP response is not valid DER: " + d.message();
    },
    Malformed(what) => {
      return "the OCSP response has a malformed " + what;
    },
    NotSuccessful(code) => {
      return "the OCSP responder gave the status " + code.to_string() + ", not successful";
    },
    NotBasic(oid) => {
      return "the OCSP response has the type " + oid + ", not id-pkix-ocsp-basic";
    },
    UnsupportedHash(oid) => {
      return "the OCSP CertID uses the hash " + oid;
    },
    NoResponse => {
      return "the OCSP response has no status for the certificate";
    },
    UnauthorizedResponder => {
      return "the OCSP response has no signature of the issuer or of a responder that the issuer delegated";
    },
    BadSignature => {
      return "the signature of the OCSP response does not verify";
    },
    Signature(v) => {
      return "the signature of the OCSP response cannot be checked: " + v.message();
    },
    _ => {
      return __status_message(e);
    }
  }
  return "";
}

function __status_message(e: OcspError): string {
  match (e) {
    NotYetValid => {
      return "the OCSP response is not valid yet";
    },
    Expired => {
      return "the OCSP response is out of date";
    },
    CertificateRevoked(at) => {
      return "the certificate was revoked at " + at.to_string() + " seconds after the epoch";
    },
    StatusUnknown => {
      return "the OCSP responder does not know the certificate";
    },
    NotStapled => {
      return "the server stapled no OCSP response";
    },
    NoIssuer => {
      return "the issuer of the certificate is not in the chain or the roots";
    },
    _ => {
      return "";
    }
  }
  return "";
}

const BASIC: string = "1.3.6.1.5.5.7.48.1.1";

const OCSP_SIGNING: string = "1.3.6.1.5.5.7.3.9";

const NO_NEXT_UPDATE_SECONDS: i64 = 86400;

function __d[T](r: Result[T, der.DerError]): Result[T, OcspError] {
  match (r) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(Der(e));
    }
  }
  return Err(Malformed("DER"));
}

function __x[T](r: Result[T, x509.X509Error]): Result[T, OcspError] {
  match (r) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(Malformed(e.message()));
    }
  }
  return Err(Malformed("certificate"));
}

function __parts(b: [u8], e: der.Element, tag: i32, what: string): Result[der.Element[], OcspError] {
  if (e.tag != tag) {
    return Err(Malformed(what));
  }
  return __d(der.children(b, e));
}

// The one element inside an EXPLICIT tag.
function __explicit(b: [u8], e: der.Element, what: string): Result[der.Element, OcspError] {
  let inner = __d(der.read(b[0:e.to], e.from))?;
  if (inner.next != e.to) {
    return Err(Malformed(what));
  }
  return Ok(inner);
}

// The ResponderID: the raw Name for byName, or the SHA-1 hash of the key for byKey.
struct Responder { by_name: boolean, id: u8[] }

struct Single { status: Status, this_update: i64, next_update: i64, has_next: boolean }

struct Basic { tbs: u8[], responder: Responder, singles: der.Element[], body: u8[], algorithm: x509.Algorithm, signature: u8[], certs: x509.Certificate[] }

// The status of `cert` in the DER OCSP response `response`, which `issuer`
// or a responder it delegated signed. now is the time of the check.
pub function check(response: [u8], cert: x509.Certificate, issuer: x509.Certificate, now: time.Timestamp): Result[Status, OcspError] {
  let basic = __basic(response)?;
  let signer = __signer(basic, issuer, now.sec)?;
  match (verify.signature_ok(signer.key, basic.algorithm, basic.tbs, basic.signature)) {
    Ok(ok) => {
      if (!ok) {
        return Err(BadSignature);
      }
    },
    Err(e) => {
      return Err(Signature(e));
    }
  }
  let single = __single_for(basic, cert, issuer)?;
  if (now.sec < single.this_update) {
    return Err(NotYetValid);
  }
  let until = single.this_update + NO_NEXT_UPDATE_SECONDS;
  if (single.has_next) {
    until = single.next_update;
  }
  if (now.sec > until) {
    return Err(Expired);
  }
  return Ok(single.status);
}

// check, and also an error for the statuses Revoked and Unknown.
pub function require_good(response: [u8], cert: x509.Certificate, issuer: x509.Certificate, now: time.Timestamp): Result[void, OcspError] {
  match (check(response, cert, issuer, now)?) {
    Good => {
      return Ok(());
    },
    Revoked(at) => {
      return Err(CertificateRevoked(at));
    },
    Unknown => {
      return Err(StatusUnknown);
    }
  }
  return Err(StatusUnknown);
}

// The issuer of `leaf` among the other certificates of `chain` and the roots:
// a certificate with the issuer's name whose key verifies the leaf.
pub function issuer_of(leaf: x509.Certificate, chain: u8[][], roots: verify.Roots): Option[x509.Certificate] {
  let candidates: x509.Certificate[] = [];
  let i = 1;
  while (i < chain.len()) {
    if let Ok(c) = x509.parse(chain[i]) {
      candidates = candidates.append(c);
    }
    i += 1;
  }
  for r in roots.certs {
    candidates = candidates.append(r);
  }
  for c in candidates {
    if (__same(c.subject.raw, leaf.issuer.raw) && verify.signature_ok(c.key, leaf.algorithm, leaf.tbs, leaf.signature).unwrap_or(false)) {
      return Some(c);
    }
  }
  return None;
}

function __basic(response: [u8]): Result[Basic, OcspError] {
  let outer = __d(der.only(response, der.SEQUENCE))?;
  let parts = __d(der.children(response, outer))?;
  if (parts.len() == 0 || parts[0].tag != 0x0a || parts[0].to - parts[0].from != 1) {
    return Err(Malformed("OCSPResponse"));
  }
  let code = response[parts[0].from] as i32;
  if (code != 0) {
    return Err(NotSuccessful(code));
  }
  if (parts.len() != 2 || parts[1].tag != der.context(0, true)) {
    return Err(Malformed("responseBytes"));
  }
  return __basic_body(__response_bytes(response, parts[1])?);
}

// The DER BasicOCSPResponse inside the [0] responseBytes of an OCSPResponse.
function __response_bytes(response: [u8], e: der.Element): Result[u8[], OcspError] {
  let rb = __parts(response, __explicit(response, e, "responseBytes")?, der.SEQUENCE, "ResponseBytes")?;
  if (rb.len() != 2 || rb[1].tag != der.OCTET_STRING) {
    return Err(Malformed("ResponseBytes"));
  }
  let kind = __d(der.oid(response, rb[0]))?;
  if (kind != BASIC) {
    return Err(NotBasic(kind));
  }
  return Ok(der.bytes(response, rb[1]));
}

function __basic_body(body: u8[]): Result[Basic, OcspError] {
  let top = __d(der.only(body, der.SEQUENCE))?;
  let parts = __d(der.children(body, top))?;
  if (parts.len() < 3 || parts.len() > 4) {
    return Err(Malformed("BasicOCSPResponse"));
  }
  let algorithm = __x(x509.algorithm(body, parts[1]))?;
  let signature = __signature(body, parts[2])?;
  let certs: x509.Certificate[] = [];
  if (parts.len() == 4) {
    certs = __certs(body, parts[3])?;
  }
  let data = __response_data(body, parts[0])?;
  return Ok(Basic { ...data, algorithm: algorithm, signature: signature, certs: certs });
}

function __signature(body: [u8], e: der.Element): Result[u8[], OcspError] {
  let sig = __d(der.bit_string(body, e))?;
  if (sig.unused != 0) {
    return Err(Malformed("signature"));
  }
  return Ok(sig.bytes);
}

function __certs(body: [u8], e: der.Element): Result[x509.Certificate[], OcspError] {
  if (e.tag != der.context(0, true)) {
    return Err(Malformed("certs"));
  }
  let certs: x509.Certificate[] = [];
  for c in __parts(body, __explicit(body, e, "certs")?, der.SEQUENCE, "certs")? {
    certs = certs.append(__x(x509.parse(der.whole(body, c)))?);
  }
  return Ok(certs);
}

// The ResponseData of a BasicOCSPResponse, with no signature and no certificates.
function __response_data(body: u8[], e: der.Element): Result[Basic, OcspError] {
  let data = __parts(body, e, der.SEQUENCE, "ResponseData")?;
  let at = __version(body, data)?;
  if (data.len() < at + 3 || data[at + 1].tag != der.GENERALIZED_TIME) {
    return Err(Malformed("ResponseData"));
  }
  let responder = __responder(body, data[at])?;
  let singles = __parts(body, data[at + 2], der.SEQUENCE, "responses")?;
  if (data.len() > at + 4 || data.len() == at + 4 && data[at + 3].tag != der.context(1, true)) {
    return Err(Malformed("ResponseData"));
  }
  return Ok(Basic { tbs: der.whole(body, e), responder: responder, singles: singles, body: body, algorithm: x509.Ed25519, signature: [], certs: [] });
}

// The count of elements before the ResponderID: 1 with the version v1, else 0.
function __version(body: [u8], data: der.Element[]): Result[i32, OcspError] {
  if (data.len() == 0 || data[0].tag != der.context(0, true)) {
    return Ok(0);
  }
  if (__d(der.small(body, __explicit(body, data[0], "version")?))? != 0) {
    return Err(Malformed("version"));
  }
  return Ok(1);
}

function __responder(b: [u8], e: der.Element): Result[Responder, OcspError] {
  if (e.tag == der.context(1, true)) {
    let name = __explicit(b, e, "ResponderID")?;
    if (name.tag != der.SEQUENCE) {
      return Err(Malformed("ResponderID"));
    }
    return Ok(Responder { by_name: true, id: der.whole(b, name) });
  }
  if (e.tag == der.context(2, true)) {
    let hash = __explicit(b, e, "ResponderID")?;
    if (hash.tag != der.OCTET_STRING || hash.to - hash.from != 20) {
      return Err(Malformed("ResponderID"));
    }
    return Ok(Responder { by_name: false, id: der.bytes(b, hash) });
  }
  return Err(Malformed("ResponderID"));
}

// The certificate whose key signed the response: the issuer, or a delegated
// responder in the response that the issuer certified for OCSP signing.
function __signer(basic: Basic, issuer: x509.Certificate, now: i64): Result[x509.Certificate, OcspError] {
  if (__names(basic.responder, issuer)?) {
    return Ok(issuer);
  }
  for c in basic.certs {
    if (__names(basic.responder, c)? && __delegated(c, issuer, now)) {
      return Ok(c);
    }
  }
  return Err(UnauthorizedResponder);
}

function __names(r: Responder, c: x509.Certificate): Result[boolean, OcspError] {
  if (r.by_name) {
    return Ok(__same(r.id, c.subject.raw));
  }
  return Ok(__same(r.id, crypto.sha1(__key_bits(c)?)));
}

function __delegated(c: x509.Certificate, issuer: x509.Certificate, now: i64): boolean {
  if (!__same(c.issuer.raw, issuer.subject.raw) || now < c.not_before || now > c.not_after) {
    return false;
  }
  if (!__has(c.ext_key_usage, OCSP_SIGNING)) {
    return false;
  }
  if (c.key_usage >= 0 && (c.key_usage & x509.KU_DIGITAL_SIGNATURE) == 0) {
    return false;
  }
  return verify.signature_ok(issuer.key, c.algorithm, c.tbs, c.signature).unwrap_or(false);
}

// The subjectPublicKey bits of a certificate: what a key hash hashes.
function __key_bits(c: x509.Certificate): Result[u8[], OcspError] {
  let parts = __parts(c.spki, __d(der.only(c.spki, der.SEQUENCE))?, der.SEQUENCE, "SubjectPublicKeyInfo")?;
  if (parts.len() != 2) {
    return Err(Malformed("SubjectPublicKeyInfo"));
  }
  return Ok(__d(der.bit_string(c.spki, parts[1]))?.bytes);
}

function __single_for(basic: Basic, cert: x509.Certificate, issuer: x509.Certificate): Result[Single, OcspError] {
  let b = basic.body;
  for s in basic.singles {
    let parts = __parts(b, s, der.SEQUENCE, "SingleResponse")?;
    if (parts.len() < 3) {
      return Err(Malformed("SingleResponse"));
    }
    if (__identifies(b, parts[0], cert, issuer)?) {
      return __single(b, parts);
    }
  }
  return Err(NoResponse);
}

function __identifies(b: [u8], e: der.Element, cert: x509.Certificate, issuer: x509.Certificate): Result[boolean, OcspError] {
  let id = __cert_id(b, e)?;
  let oid = __hash_oid(b, id[0])?;
  if (!__same(der.bytes(b, id[3]), cert.serial)) {
    return Ok(false);
  }
  let name_hash = __hash(oid, cert.issuer.raw)?;
  let key_hash = __hash(oid, __key_bits(issuer)?)?;
  return Ok(__same(der.bytes(b, id[1]), name_hash) && __same(der.bytes(b, id[2]), key_hash));
}

function __cert_id(b: [u8], e: der.Element): Result[der.Element[], OcspError] {
  let id = __parts(b, e, der.SEQUENCE, "CertID")?;
  if (id.len() != 4 || id[1].tag != der.OCTET_STRING || id[2].tag != der.OCTET_STRING || id[3].tag != der.INTEGER) {
    return Err(Malformed("CertID"));
  }
  return Ok(id);
}

function __hash_oid(b: [u8], e: der.Element): Result[string, OcspError] {
  let alg = __parts(b, e, der.SEQUENCE, "CertID hashAlgorithm")?;
  if (alg.len() == 0 || alg.len() > 2) {
    return Err(Malformed("CertID hashAlgorithm"));
  }
  return __d(der.oid(b, alg[0]));
}

function __hash(oid: string, data: [u8]): Result[u8[], OcspError] {
  if (oid == "1.3.14.3.2.26") {
    return Ok(crypto.sha1(data));
  }
  if (oid == "2.16.840.1.101.3.4.2.1") {
    return Ok(crypto.sha256(data));
  }
  if (oid == "2.16.840.1.101.3.4.2.2") {
    return Ok(crypto.sha384(data));
  }
  if (oid == "2.16.840.1.101.3.4.2.3") {
    return Ok(crypto.sha512(data));
  }
  return Err(UnsupportedHash(oid));
}

function __single(b: [u8], parts: der.Element[]): Result[Single, OcspError] {
  let status = __status(b, parts[1])?;
  if (parts[2].tag != der.GENERALIZED_TIME) {
    return Err(Malformed("thisUpdate"));
  }
  let single = Single { status: status, this_update: __d(der.time(b, parts[2]))?, next_update: 0i64, has_next: false };
  if (parts.len() > 3 && parts[3].tag == der.context(0, true)) {
    single = Single { ...single, next_update: __next_update(b, parts[3])?, has_next: true };
  }
  return __single_end(parts, single);
}

// Only singleExtensions can follow nextUpdate, and nextUpdate is not before thisUpdate.
function __single_end(parts: der.Element[], single: Single): Result[Single, OcspError] {
  let at = 3;
  if (single.has_next) {
    at = 4;
  }
  if (parts.len() > at + 1 || parts.len() == at + 1 && parts[at].tag != der.context(1, true) || single.has_next && single.next_update < single.this_update) {
    return Err(Malformed("SingleResponse"));
  }
  return Ok(single);
}

function __next_update(b: [u8], e: der.Element): Result[i64, OcspError] {
  let next = __explicit(b, e, "nextUpdate")?;
  if (next.tag != der.GENERALIZED_TIME) {
    return Err(Malformed("nextUpdate"));
  }
  return __d(der.time(b, next));
}

function __status(b: [u8], e: der.Element): Result[Status, OcspError] {
  if (e.tag == der.context(1, true)) {
    return __revoked(b, e);
  }
  if (e.to != e.from) {
    return Err(Malformed("CertStatus"));
  }
  if (e.tag == der.context(0, false)) {
    return Ok(Good);
  }
  if (e.tag == der.context(2, false)) {
    return Ok(Unknown);
  }
  return Err(Malformed("CertStatus"));
}

function __revoked(b: [u8], e: der.Element): Result[Status, OcspError] {
  let info = __d(der.children(b, e))?;
  if (info.len() == 0 || info.len() > 2 || info[0].tag != der.GENERALIZED_TIME) {
    return Err(Malformed("RevokedInfo"));
  }
  return Ok(Revoked(__d(der.time(b, info[0]))?));
}

function __has(v: string[], x: string): boolean {
  for s in v {
    if (s == x) {
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
