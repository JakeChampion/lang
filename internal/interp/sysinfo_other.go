//go:build !linux && !darwin

package interp

// unameFields has nothing to read on a platform with no utsname record
// and no sysctl for one; the five fields come back empty, which is the
// same answer a kernel that refuses the call gives.
func unameFields() [5]string {
	return [5]string{}
}
