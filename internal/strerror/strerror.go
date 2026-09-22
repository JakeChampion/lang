// Package strerror is the one errno table every Fern runtime reports
// `IoError.Other(path, message)` from: the message is glibc's
// strerror text for the errno, on every target, because matching what
// C programs print is the whole reason the field exists (#8265).
//
// Each entry carries the errno's number on each OS the runtime issues
// syscalls to. Linux and Darwin number their errnos differently and
// WASI preview 1 differently again, so the table is keyed by name and
// resolved per target.
//
// The WORDING is resolved per target too. Text is glibc's; darwinText
// overrides the sixteen errnos whose Darwin libc message differs. A C
// program prints strerror(3) verbatim, so a Darwin build that said
// "Invalid cross-device link" where the platform says "Cross-device
// link" would diverge from every other binary on the machine.
//
// The three asm backends emit the table as a compare ladder over
// `.rodata` literals, wasm as a ladder over data-segment literals, the
// interpreter reads it directly, and the self-host compiler carries a
// copy in examples/self_host/asmcore.fern that selfhost_parity_test.go
// pins to this one entry for entry.
package strerror

import "strconv"

// Entry is one errno: its C name, glibc's strerror text, and its number
// on each OS. A zero number means the OS has no separate errno of that
// name (EOPNOTSUPP is ENOTSUP's alias on Linux and WASI). Text is the
// wording everywhere except the Darwin errnos darwinText overrides.
type Entry struct {
	Name   string
	Text   string
	Linux  int
	Darwin int
	Wasi   int
}

// The target OS names Number / Text / Dense resolve by. Linux and
// Darwin are runtime.GOOS spellings; Wasi is preview-1 numbering.
const (
	Linux  = "linux"
	Darwin = "darwin"
	Wasi   = "wasi"
)

// Number is the errno's number on os, or 0 when os has none.
func (e Entry) Number(os string) int {
	switch os {
	case Linux:
		return e.Linux
	case Darwin:
		return e.Darwin
	case Wasi:
		return e.Wasi
	}
	return 0
}

// darwinText is the Darwin libc's wording for the errnos whose message
// is not glibc's, checked against the host by TestTextMatchesHostLibc:
// an entry missing here whose wording differs fails that test, so the
// list cannot silently fall behind a platform.
var darwinText = map[string]string{
	"ENXIO":         "Device not configured",
	"EBUSY":         "Resource busy",
	"EXDEV":         "Cross-device link",
	"ENODEV":        "Operation not supported by device",
	"ERANGE":        "Result too large",
	"EAFNOSUPPORT":  "Address family not supported by protocol family",
	"EADDRNOTAVAIL": "Can't assign requested address",
	"EISCONN":       "Socket is already connected",
	"ENOTCONN":      "Socket is not connected",
	"ETIMEDOUT":     "Operation timed out",
	"EDQUOT":        "Disc quota exceeded",
	"ESTALE":        "Stale NFS file handle",
	"EOVERFLOW":     "Value too large to be stored in data type",
	"EILSEQ":        "Illegal byte sequence",
	"EOPNOTSUPP":    "Operation not supported on socket",
	"EOWNERDEAD":    "Previous owner died",
}

// TextFor is the errno's strerror text on os.
func (e Entry) TextFor(os string) string {
	if os == Darwin {
		if t, ok := darwinText[e.Name]; ok {
			return t
		}
	}
	return e.Text
}

// Table lists every errno the runtime's syscalls (open / read / write /
// close / stat / unlink / mkdir / getdents / socket family / spawn) can
// return, in Linux numbering order. An errno outside it is reported as
// Unknown(errno), which is also what glibc prints.
var Table = []Entry{
	{"EPERM", "Operation not permitted", 1, 1, 63},
	{"ENOENT", "No such file or directory", 2, 2, 44},
	{"ESRCH", "No such process", 3, 3, 71},
	{"EINTR", "Interrupted system call", 4, 4, 27},
	{"EIO", "Input/output error", 5, 5, 29},
	{"ENXIO", "No such device or address", 6, 6, 60},
	{"E2BIG", "Argument list too long", 7, 7, 1},
	{"ENOEXEC", "Exec format error", 8, 8, 45},
	{"EBADF", "Bad file descriptor", 9, 9, 8},
	{"ECHILD", "No child processes", 10, 10, 12},
	{"EAGAIN", "Resource temporarily unavailable", 11, 35, 6},
	{"ENOMEM", "Cannot allocate memory", 12, 12, 48},
	{"EACCES", "Permission denied", 13, 13, 2},
	{"EFAULT", "Bad address", 14, 14, 21},
	{"EBUSY", "Device or resource busy", 16, 16, 10},
	{"EEXIST", "File exists", 17, 17, 20},
	{"EXDEV", "Invalid cross-device link", 18, 18, 75},
	{"ENODEV", "No such device", 19, 19, 43},
	{"ENOTDIR", "Not a directory", 20, 20, 54},
	{"EISDIR", "Is a directory", 21, 21, 31},
	{"EINVAL", "Invalid argument", 22, 22, 28},
	{"ENFILE", "Too many open files in system", 23, 23, 41},
	{"EMFILE", "Too many open files", 24, 24, 33},
	{"ENOTTY", "Inappropriate ioctl for device", 25, 25, 59},
	{"ETXTBSY", "Text file busy", 26, 26, 74},
	{"EFBIG", "File too large", 27, 27, 22},
	{"ENOSPC", "No space left on device", 28, 28, 51},
	{"ESPIPE", "Illegal seek", 29, 29, 70},
	{"EROFS", "Read-only file system", 30, 30, 69},
	{"EMLINK", "Too many links", 31, 31, 34},
	{"EPIPE", "Broken pipe", 32, 32, 64},
	{"ERANGE", "Numerical result out of range", 34, 34, 68},
	{"EDEADLK", "Resource deadlock avoided", 35, 11, 16},
	{"ENAMETOOLONG", "File name too long", 36, 63, 37},
	{"ENOLCK", "No locks available", 37, 77, 46},
	{"ENOSYS", "Function not implemented", 38, 78, 52},
	{"ENOTEMPTY", "Directory not empty", 39, 66, 55},
	{"ELOOP", "Too many levels of symbolic links", 40, 62, 32},
	{"EOVERFLOW", "Value too large for defined data type", 75, 84, 61},
	{"EILSEQ", "Invalid or incomplete multibyte or wide character", 84, 92, 25},
	{"ENOTSOCK", "Socket operation on non-socket", 88, 38, 57},
	{"EMSGSIZE", "Message too long", 90, 40, 35},
	{"EPROTOTYPE", "Protocol wrong type for socket", 91, 41, 67},
	{"ENOPROTOOPT", "Protocol not available", 92, 42, 50},
	{"EPROTONOSUPPORT", "Protocol not supported", 93, 43, 66},
	{"ENOTSUP", "Operation not supported", 95, 45, 58},
	{"EAFNOSUPPORT", "Address family not supported by protocol", 97, 47, 5},
	{"EADDRINUSE", "Address already in use", 98, 48, 3},
	{"EADDRNOTAVAIL", "Cannot assign requested address", 99, 49, 4},
	{"ENETDOWN", "Network is down", 100, 50, 38},
	{"ENETUNREACH", "Network is unreachable", 101, 51, 40},
	{"ENETRESET", "Network dropped connection on reset", 102, 52, 39},
	{"ECONNABORTED", "Software caused connection abort", 103, 53, 13},
	{"ECONNRESET", "Connection reset by peer", 104, 54, 15},
	{"ENOBUFS", "No buffer space available", 105, 55, 42},
	{"EISCONN", "Transport endpoint is already connected", 106, 56, 30},
	{"ENOTCONN", "Transport endpoint is not connected", 107, 57, 53},
	{"ETIMEDOUT", "Connection timed out", 110, 60, 73},
	{"ECONNREFUSED", "Connection refused", 111, 61, 14},
	{"EHOSTUNREACH", "No route to host", 113, 65, 23},
	{"EALREADY", "Operation already in progress", 114, 37, 7},
	{"EINPROGRESS", "Operation now in progress", 115, 36, 26},
	{"ESTALE", "Stale file handle", 116, 70, 72},
	{"EDQUOT", "Disk quota exceeded", 122, 69, 19},
	{"ECANCELED", "Operation canceled", 125, 89, 11},
	{"EOWNERDEAD", "Owner died", 130, 105, 62},
	{"ENOTRECOVERABLE", "State not recoverable", 131, 104, 56},
	{"EOPNOTSUPP", "Operation not supported", 0, 102, 0},
}

// UnknownPrefix precedes the number in the text for an errno outside
// Table; the runtimes build that text from it at run time. This is
// glibc's spelling — Darwin's is DarwinUnknownPrefix, which carries a
// colon (Apple's Libc builds it in string/FreeBSD/strerror.c's
// __errstr, as UPREFIX then ": " then the digits).
const (
	UnknownPrefix       = "Unknown error "
	DarwinUnknownPrefix = "Unknown error: "
)

// UnknownPrefixFor is the prefix on os.
func UnknownPrefixFor(os string) string {
	if os == Darwin {
		return DarwinUnknownPrefix
	}
	return UnknownPrefix
}

// Unknown is the text os gives for an errno outside Table.
func Unknown(os string, errno int) string {
	return UnknownPrefixFor(os) + strconv.Itoa(errno)
}

// Text is strerror(errno) on os.
func Text(os string, errno int) string {
	for _, e := range Table {
		if n := e.Number(os); n != 0 && n == errno {
			return e.TextFor(os)
		}
	}
	return Unknown(os, errno)
}

// Number is the errno named name on os, or 0 when os has none.
func Number(os, name string) int {
	for _, e := range Table {
		if e.Name == name {
			return e.Number(os)
		}
	}
	return 0
}

// Dense is the table as an errno-indexed slice for os: Dense(os)[n] is
// the text for errno n, "" where n has no entry. Its length is one past
// the largest errno os numbers, so a backend can emit it as a ladder or
// a lookup table without knowing the numbering.
func Dense(os string) []string {
	max := 0
	for _, e := range Table {
		if n := e.Number(os); n > max {
			max = n
		}
	}
	out := make([]string, max+1)
	for _, e := range Table {
		if n := e.Number(os); n != 0 {
			out[n] = e.TextFor(os)
		}
	}
	return out
}

// WasiErrorCode pairs a `wasi:filesystem/types.error-code` case with
// the preview-1 errno it stands for.
type WasiErrorCode struct {
	Code  string
	Errno string
}

// WasiErrorCodes is the error-code enum in discriminant order
// (component.WasiFilesystemErrorCodeNames, from the vendored WIT). A
// preview-2 host reports a failure as one of these bytes, and the
// runtime translates it back to the errno the preview-1 classifier
// understands.
var WasiErrorCodes = []WasiErrorCode{
	{"access", "EACCES"},
	{"would-block", "EAGAIN"},
	{"already", "EALREADY"},
	{"bad-descriptor", "EBADF"},
	{"busy", "EBUSY"},
	{"deadlock", "EDEADLK"},
	{"quota", "EDQUOT"},
	{"exist", "EEXIST"},
	{"file-too-large", "EFBIG"},
	{"illegal-byte-sequence", "EILSEQ"},
	{"in-progress", "EINPROGRESS"},
	{"interrupted", "EINTR"},
	{"invalid", "EINVAL"},
	{"io", "EIO"},
	{"is-directory", "EISDIR"},
	{"loop", "ELOOP"},
	{"too-many-links", "EMLINK"},
	{"message-size", "EMSGSIZE"},
	{"name-too-long", "ENAMETOOLONG"},
	{"no-device", "ENODEV"},
	{"no-entry", "ENOENT"},
	{"no-lock", "ENOLCK"},
	{"insufficient-memory", "ENOMEM"},
	{"insufficient-space", "ENOSPC"},
	{"not-directory", "ENOTDIR"},
	{"not-empty", "ENOTEMPTY"},
	{"not-recoverable", "ENOTRECOVERABLE"},
	{"unsupported", "ENOTSUP"},
	{"no-tty", "ENOTTY"},
	{"no-such-device", "ENXIO"},
	{"overflow", "EOVERFLOW"},
	{"not-permitted", "EPERM"},
	{"pipe", "EPIPE"},
	{"read-only", "EROFS"},
	{"invalid-seek", "ESPIPE"},
	{"text-file-busy", "ETXTBSY"},
	{"cross-device", "EXDEV"},
}

// WasiErrnoOfCode is the preview-1 errno for a preview-2 error-code
// discriminant. A discriminant outside the enum — a host newer than the
// vendored WIT — reads as ENOENT, the same default the translation has
// always had.
func WasiErrnoOfCode(code int) int {
	if code < 0 || code >= len(WasiErrorCodes) {
		return Number(Wasi, "ENOENT")
	}
	return Number(Wasi, WasiErrorCodes[code].Errno)
}
