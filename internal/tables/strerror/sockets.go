package strerror

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
