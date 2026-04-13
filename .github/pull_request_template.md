## Summary

<!-- What does this PR do? Link related issues with "Fixes #123" -->



## Changes

<!-- Key changes, organized by area. Delete empty sections. -->

-

## Design Decisions

<!-- Explain non-obvious choices. Delete section if straightforward. -->



## Example

<!-- For public-API changes: show the Go snippet. Delete if not applicable. -->

```go

```

## Test Plan

- [ ] `go build ./...` passes
- [ ] `go vet ./...` is clean
- [ ] `go test -race -count=1 ./...` passes (CI covers linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64)
- [ ] If changing the validator or scheme contract: JVM wire-parity test (`TestIntegrationFullFlow`) still matches
- [ ] If adding a new rule kind or scheme field: JSON round-trip test (`TestWriteSchemeJSON`) covers it
- [ ] If changing public API: README snippet still compiles
- [ ] If changing LION v4 output: existing integration fixtures still match the JVM SDK
