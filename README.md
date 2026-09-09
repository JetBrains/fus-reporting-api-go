# fus-reporting-api-go

> [!WARNING]
> This library is maintained for JetBrains-owned products and services only.
> No public support, compatibility guarantees, or semantic versioning commitment is provided.
> APIs and behavior may change without notice.

Go client for JetBrains Feature Usage Statistics (FUS). Wire-compatible with the [JVM implementation](https://mvnrepository.com/artifact/com.jetbrains.fus.reporting/api).

## Installation

```bash
go get github.com/JetBrains/fus-reporting-api-go
```

## Quick start

This example uses the low-level runtime metadata API. For new integrations and
registration schema generation, use the event declarations below.

```go
import fus "github.com/JetBrains/fus-reporting-api-go"

// 1. Define your scheme — used at runtime for validation and marshalable
//    to schema.json for AP metadata registration.
scheme := &fus.Scheme{
    Version: "1",
    Rules: &fus.SchemeRules{
        Enums:   map[string][]string{"boolean": {"true", "false"}},
        Regexps: map[string]string{"version": `\d+\.\d+\.\d+`},
    },
    Groups: []fus.GroupSchema{
        {
            ID: "cli.command",
            Rules: &fus.SchemeRules{
                EventID: []string{fus.EnumExpr("executed")},
                EventData: map[string][]string{
                    "command":     {fus.EnumExpr("build", "run", "test")},
                    "duration_ms": {fus.RegexpExpr(`\d+`)},
                    "has_error":   {fus.EnumRefExpr("boolean")},
                },
            },
        },
    },
}
validator, err := fus.NewValidator(scheme)
if err != nil { /* ... */ }

// 2. Create a logger — validator is required, config fetch must succeed.
ctx := context.Background()
logger, err := fus.NewLogger(ctx, fus.RecorderConfig{
    RecorderID:      "FUS",       // assigned by Data Office
    RecorderVersion: 1,
    ProductCode:     "TCC",       // registered in FUS
    BuildVersion:    "0.5.0",
    DataDir:         "~/.config/myproduct/",
}, fus.WithValidator(validator))
if err != nil { /* ... */ }

// 3. Track events — appends to disk, sub-millisecond, never blocks.
logger.Track(
    fus.EventGroup{ID: "cli.command", Version: 1, State: false},
    "executed",
    map[string]any{"command": "build", "duration_ms": 1234, "has_error": false},
)

// 4. Flush on shutdown.
logger.Close(ctx)
```

See `example_test.go` for a runnable version that prints the full JSON payload.

## How it works

```text
Track()  ──▶  validate  ──▶  escape  ──▶  buffer (JSONL on disk)  ──▶  Flush()  ──▶  FUS
```

- **Track** appends one JSON line to a local buffer file. No network call.
- **Validate** rewrites events so only scheme-approved keys and values reach the wire.
  Unrecognized keys/values are replaced with sentinels; out-of-range events are dropped.
- **Escape** normalizes strings for wire safety.
- **Buffer** survives process restarts and network failures.
- **Flush** sends buffered events over HTTP, re-queuing unsent batches on failure.

The SDK is fail-closed: `NewLogger` refuses to start without a valid anonymization salt
(fetched from the config server or loaded from a local cache) and a configured validator.

## Validation rules

The validator is required. It supports the same rule vocabulary as the JVM `SensitiveDataValidator`:

| Rule | Helper |
|---|---|
| Inline enum | `fus.EnumExpr("a", "b", "c")` |
| Enum reference | `fus.EnumRefExpr("name")` |
| Inline regexp | `fus.RegexpExpr(pattern)` |
| Regexp reference | `fus.RegexpRefExpr("name")` |
| Inline int range | `fus.RangeExpr(from, to)` |
| Range reference | `fus.RangeRefExpr("name")` |

References resolve against the scheme's top-level `Rules` block.

For tests, `fus.NewPermissiveValidator()` passes every event unchanged. Do not use in production.

## Generating `schema.json`

`Scheme` is JSON-marshalable in the AP metadata format. Ship a generator alongside your product:

```go
package main

import (
    "os"
    fus "github.com/JetBrains/fus-reporting-api-go"
    "your.product/internal/telemetry"
)

func main() {
    f, _ := os.Create("schema.json")
    defer f.Close()
    if err := fus.WriteSchemeJSON(telemetry.Scheme, f); err != nil {
        panic(err)
    }
}
```

The same `Scheme` value goes to both `NewValidator` and `WriteSchemeJSON`, so the
serialized runtime metadata matches the local validator. For event registration,
generate `events-scheme.json` from declarations as described below.

## Declaring events and generating registration metadata

Use `Definition` for event-specific fields, like Kotlin's `EventLogGroup`.
`Scheme` remains the group-level AP/CDN validation format.

```go
auth := fus.GroupDefinition{
    ID: "cli.auth", Version: 1, Description: "Authentication",
    Events: []fus.EventDefinition{
        {
            ID: "login.completed", Description: "Login result",
            Fields: []fus.FieldDefinition{
                {Path: "method", Rules: []string{fus.EnumExpr("token", "guest")}},
            },
        },
        {
            ID: "token.loaded", Description: "Token source",
            Fields: []fus.FieldDefinition{
                {Path: "source", Rules: []string{fus.EnumExpr("env", "keyring")}},
            },
        },
    },
}
definition := &fus.Definition{Groups: []fus.GroupDefinition{auth}}

registration, err := definition.BuildEventsScheme(cfg, buildNumber)
if err != nil { /* handle error */ }
err = fus.WriteEventsSchemeJSON(registration, output)
if err != nil { /* handle error */ }

fallback, err := definition.BuildValidationScheme()
if err != nil { /* handle error */ }
validator, err := fus.NewValidator(fallback)
if err != nil { /* handle error */ }
```

Registration preserves each event's fields, descriptions, types and rule references.
`Definition.Rules` supplies fallback definitions; referenced rules must also exist
in AP metadata. Use inline rules when a shared AP rule is not intended.
`Anonymized: true` emits `{regexp#hash}` and per-event fallback anonymization.
Configure `NewAnonymizer` from the selected runtime scheme as usual.

Fallback rules are unioned per group and restricted to its declared version.
Use CDN metadata when available; fallback generation does not reproduce Data
Office history/build ranges. Arrays and nested fields work in registration
output but are rejected by fallback generation because the runtime validator
does not support them yet. `Track` and runtime validation are unchanged.

The old `fus.BuildEventsScheme(*Scheme, ...)` is deprecated: flattened metadata
cannot recover field ownership. Migrate using actual event call sites.

## For JetBrains teams

Coordinate recorder setup, product registration, and event metadata with the Data Office.
See `config.go` for endpoint configuration and regional options.
