# TLS failure values

This module defines common error types for TLS connections.

## Model

A TLS error retains its concrete protocol or transport cause. A failure can also retain alert bytes for the peer.

## Contracts

Message text explains the error and preserves its kind. A caller can send an alert when the failed protocol step supplies one.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum TlsError>>

<<4 pub function (e: TlsError) to_string(): string>>

<<5 impl error.Error for TlsError>>

<<6 pub function (e: TlsError) message(): string>>

<<7 function __later_message(e: TlsError): string>>

<<8 pub struct Failure>>

<<9 pub function (f: Failure) message(): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module defines common error types for TLS connections.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/dns";
import "std/net";
import "std/tls/handshake";
import "std/tls/verify";
import "std/error";
import "core/cmp";
```

### `3 pub enum TlsError`

```fern
<<3 pub enum TlsError>>=
pub enum TlsError {
  HandshakeFailed(handshake.HandshakeError),
  Untrusted(verify.VerifyError),
  CertificateMissing,
  SignatureInvalid,
  SchemeMismatch,
  SigningFailed,
  NoRoots,
  Unresolved(dns.DnsError),
  NetFailed(net.NetError),
  HandshakeTimeout,
  ClosedEarly,
  RecordCut,
  OffloadRefused(i32),
  KeysRefused(i32),
  KernelReadFailed(i32),
}
```

### `4 pub function (e: TlsError) to_string(): string`

```fern
<<4 pub function (e: TlsError) to_string(): string>>=
pub function (e: TlsError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for TlsError`

```fern
<<5 impl error.Error for TlsError>>=
impl error.Error for TlsError {}

impl cmp.Display for TlsError {}
```

### `6 pub function (e: TlsError) message(): string`

```fern
<<6 pub function (e: TlsError) message(): string>>=
pub function (e: TlsError) message(): string {
  match (e) {
    HandshakeFailed(h) => {
      return h.message();
    },
    Untrusted(v) => {
      return "the peer's certificate is not trusted: " + v.message();
    },
    CertificateMissing => {
      return "the peer sent no certificate";
    },
    SignatureInvalid => {
      return "the peer's CertificateVerify does not verify";
    },
    SchemeMismatch => {
      return "the peer's CertificateVerify names a scheme its key cannot make";
    },
    SigningFailed => {
      return "the key could not sign the CertificateVerify";
    },
    _ => {
      return __later_message(e);
    }
  }
  return "";
}
```

### `7 function __later_message(e: TlsError): string`

```fern
<<7 function __later_message(e: TlsError): string>>=
function __later_message(e: TlsError): string {
  match (e) {
    NoRoots => {
      return "no root certificates: none at SSL_CERT_FILE or the system's bundle paths";
    },
    Unresolved(d) => {
      return d.message();
    },
    NetFailed(n) => {
      return n.message();
    },
    HandshakeTimeout => {
      return "the handshake did not finish in time";
    },
    ClosedEarly => {
      return "the peer closed the connection during the handshake";
    },
    RecordCut => {
      return "the peer closed the connection inside a record";
    },
    _ => {
      return __kernel_message(e);
    }
  }
  return "";
}

function __kernel_message(e: TlsError): string {
  match (e) {
    OffloadRefused(n) => {
      return "the kernel's TLS record layer refused the transmit keys (errno " + n.to_string() + ")";
    },
    KeysRefused(n) => {
      return "the kernel's TLS record layer refused new keys (errno " + n.to_string() + "); Linux before 6.14 takes one key per direction";
    },
    KernelReadFailed(n) => {
      return "the kernel's TLS record layer failed a read (errno " + n.to_string() + ")";
    },
    _ => {
      return "";
    }
  }
  return "";
}
```

### `8 pub struct Failure`

```fern
<<8 pub struct Failure>>=
pub struct Failure { error: TlsError, alert: u8[] }
```

### `9 pub function (f: Failure) message(): string`

```fern
<<9 pub function (f: Failure) message(): string>>=
pub function (f: Failure) message(): string {
  return f.error.message();
}
```
