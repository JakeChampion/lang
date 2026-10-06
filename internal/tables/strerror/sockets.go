package strerror

// WasiSocketErrorCodes follows wasi:sockets/network@0.2.0's error-code enum.
// These are discriminants, not errnos: in particular unknown is zero. The
// legacy integer socket APIs translate them to positive Preview 1 errnos before
// negating them, so every Err stays negative and shares strerror's namespace.
// Several coarse WIT cases have no exact errno: invalid states use EINVAL,
// remote-unreachable uses EHOSTUNREACH, and resolver failures use ENOENT,
// EAGAIN or EIO. This conversion does not recover detail the host did not send.
// Source: https://github.com/WebAssembly/wasi-sockets/blob/v0.2.0/wit/network.wit
var WasiSocketErrorCodes = []WasiErrorCode{
	{"unknown", "EIO"},
	{"access-denied", "EACCES"},
	{"not-supported", "ENOTSUP"},
	{"invalid-argument", "EINVAL"},
	{"out-of-memory", "ENOMEM"},
	{"timeout", "ETIMEDOUT"},
	{"concurrency-conflict", "EALREADY"},
	{"not-in-progress", "EINVAL"},
	{"would-block", "EAGAIN"},
	{"invalid-state", "EINVAL"},
	{"new-socket-limit", "EMFILE"},
	{"address-not-bindable", "EADDRNOTAVAIL"},
	{"address-in-use", "EADDRINUSE"},
	{"remote-unreachable", "EHOSTUNREACH"},
	{"connection-refused", "ECONNREFUSED"},
	{"connection-reset", "ECONNRESET"},
	{"connection-aborted", "ECONNABORTED"},
	{"datagram-too-large", "EMSGSIZE"},
	{"name-unresolvable", "ENOENT"},
	{"temporary-resolver-failure", "EAGAIN"},
	{"permanent-resolver-failure", "EIO"},
}

func WasiSocketErrno(code int) int {
	if code < 0 || code >= len(WasiSocketErrorCodes) {
		return Number(Wasi, "EIO")
	}
	return Number(Wasi, WasiSocketErrorCodes[code].Errno)
}
