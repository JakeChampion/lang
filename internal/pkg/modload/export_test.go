package modload

// SamePackageForTest exposes the unexported samePackage to the external
// _test package so the pub(package) visibility rule can be unit-tested.
var SamePackageForTest = samePackage
