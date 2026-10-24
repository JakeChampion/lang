//go:build !linux && !darwin

package interp

func unameFields() [5]string {
	return [5]string{}
}
