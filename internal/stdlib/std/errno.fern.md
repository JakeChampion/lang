# Portable runtime error numbers

This module provides error numbers for runtime I/O failures.

## Model

Public constants use the runtime error-number convention. Host conversion tables translate Darwin and WASI values into that convention.

## Contracts

The numeric tables must remain aligned with the shared strerror table. Host error numbers must pass through conversion before comparison with a public constant.

## Executable example

```fern test name=std-errno-example
import "std/errno";

function main(): i32 {
  if (errno.ENOENT != 2 || errno.EINTR != 4) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub const EPERM: i32>>

<<3 pub const ENOTRECOVERABLE: i32>>

<<4 pub function of(e: IoError): i32>>

<<5 function __from_table(host: i32[], e: i32): i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides error numbers for runtime I/O failures.
```

### `2 pub const EPERM: i32`

```fern
<<2 pub const EPERM: i32>>=
pub const EPERM: i32 = 1;

pub const ENOENT: i32 = 2;

pub const ESRCH: i32 = 3;

pub const EINTR: i32 = 4;

pub const EIO: i32 = 5;

pub const ENXIO: i32 = 6;

pub const E2BIG: i32 = 7;

pub const ENOEXEC: i32 = 8;

pub const EBADF: i32 = 9;

pub const ECHILD: i32 = 10;

pub const EAGAIN: i32 = 11;

pub const ENOMEM: i32 = 12;

pub const EACCES: i32 = 13;

pub const EFAULT: i32 = 14;

pub const EBUSY: i32 = 16;

pub const EEXIST: i32 = 17;

pub const EXDEV: i32 = 18;

pub const ENODEV: i32 = 19;

pub const ENOTDIR: i32 = 20;

pub const EISDIR: i32 = 21;

pub const EINVAL: i32 = 22;

pub const ENFILE: i32 = 23;

pub const EMFILE: i32 = 24;

pub const ENOTTY: i32 = 25;

pub const ETXTBSY: i32 = 26;

pub const EFBIG: i32 = 27;

pub const ENOSPC: i32 = 28;

pub const ESPIPE: i32 = 29;

pub const EROFS: i32 = 30;

pub const EMLINK: i32 = 31;

pub const EPIPE: i32 = 32;

pub const ERANGE: i32 = 34;

pub const EDEADLK: i32 = 35;

pub const ENAMETOOLONG: i32 = 36;

pub const ENOLCK: i32 = 37;

pub const ENOSYS: i32 = 38;

pub const ENOTEMPTY: i32 = 39;

pub const ELOOP: i32 = 40;

pub const ENODATA: i32 = 61;

pub const ENOATTR: i32 = 61;

pub const EOVERFLOW: i32 = 75;

pub const EILSEQ: i32 = 84;

pub const ENOTSOCK: i32 = 88;

pub const EMSGSIZE: i32 = 90;

pub const EPROTOTYPE: i32 = 91;

pub const ENOPROTOOPT: i32 = 92;

pub const EPROTONOSUPPORT: i32 = 93;

pub const ENOTSUP: i32 = 95;

pub const EAFNOSUPPORT: i32 = 97;

pub const EADDRINUSE: i32 = 98;

pub const EADDRNOTAVAIL: i32 = 99;

pub const ENETDOWN: i32 = 100;

pub const ENETUNREACH: i32 = 101;

pub const ENETRESET: i32 = 102;

pub const ECONNABORTED: i32 = 103;

pub const ECONNRESET: i32 = 104;

pub const ENOBUFS: i32 = 105;

pub const EISCONN: i32 = 106;

pub const ENOTCONN: i32 = 107;

pub const ETIMEDOUT: i32 = 110;

pub const ECONNREFUSED: i32 = 111;

pub const EHOSTUNREACH: i32 = 113;

pub const EALREADY: i32 = 114;

pub const EINPROGRESS: i32 = 115;

pub const ESTALE: i32 = 116;

pub const EDQUOT: i32 = 122;

pub const ECANCELED: i32 = 125;

pub const EOWNERDEAD: i32 = 130;
```

### `3 pub const ENOTRECOVERABLE: i32`

```fern
<<3 pub const ENOTRECOVERABLE: i32>>=
pub const ENOTRECOVERABLE: i32 = 131;

pub const EOPNOTSUPP: i32 = 95;
```

### `4 pub function of(e: IoError): i32`

```fern
<<4 pub function of(e: IoError): i32>>=
pub function of(e: IoError): i32 {
  match (e) {
    NotFound(_) => {
      return ENOENT;
    },
    PermissionDenied(_) => {
      return EACCES;
    },
    AlreadyExists(_) => {
      return EEXIST;
    },
    InvalidUtf8(_) => {
      return EILSEQ;
    },
    Interrupted => {
      return EINTR;
    },
    Unsupported => {
      return EOPNOTSUPP;
    },
    Other(_, _, n) => {
      return n;
    }
  }
}

pub function from_host(n: i32): i32 {
  let e: i32 = n;
  if (e == 0 - 2147483647 - 1) {
    e = 2147483647;
  } else if (e < 0) {
    e = 0 - e;
  }
  let os: string = target_os();
  if (os == "darwin") {
    return __from_table(__darwin_errnos(), e);
  }
  if (os == "wasi" || os == "wasi-http") {
    return __from_table(__wasi_errnos(), e);
  }
  return e;
}
```

### `5 function __from_table(host: i32[], e: i32): i32`

```fern
<<5 function __from_table(host: i32[], e: i32): i32>>=
function __from_table(host: i32[], e: i32): i32 {
  if (e == 0) {
    return 0;
  }
  let i: i32 = 0;
  while (i < host.len()) {
    if (host[i] == e) {
      return __carried_errnos()[i];
    }
    i = i + 1;
  }
  return 0;
}

function __darwin_errnos(): i32[] {
  return [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 35, 12, 13, 14, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 34, 11, 63, 77, 78, 66, 62, 96, 93, 84, 92, 38, 40, 41, 42, 43, 45, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 60, 61, 65, 37, 36, 70, 69, 89, 105, 104, 102];
}

function __wasi_errnos(): i32[] {
  return [63, 44, 71, 27, 29, 60, 1, 45, 8, 12, 6, 48, 2, 21, 10, 20, 75, 43, 54, 31, 28, 41, 33, 59, 74, 22, 51, 70, 69, 34, 64, 68, 16, 37, 46, 52, 55, 32, 0, 0, 61, 25, 57, 35, 67, 50, 66, 58, 5, 3, 4, 38, 40, 39, 13, 15, 42, 30, 53, 73, 14, 23, 7, 26, 72, 19, 11, 62, 56, 0];
}

function __carried_errnos(): i32[] {
  return [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 34, 35, 36, 37, 38, 39, 40, 61, 61, 75, 84, 88, 90, 91, 92, 93, 95, 97, 98, 99, 100, 101, 102, 103, 104, 105, 106, 107, 110, 111, 113, 114, 115, 116, 122, 125, 130, 131, 95];
}
```
