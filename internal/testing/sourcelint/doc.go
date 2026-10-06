// Package sourcelint holds fast, dependency-free repo-hygiene checks that run
// in the ordinary `go test ./...` lane. It has no runtime code — the checks
// live in the package's *_test.go files.
package sourcelint
