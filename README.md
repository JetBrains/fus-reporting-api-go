# fus-reporting-api-go

> [!WARNING]
> This repository is public, but it is intended for use only by JetBrains-owned products and services.
> External use is not supported. APIs, behavior, and compatibility may change without notice.

Go client for JetBrains Feature Usage Statistics (FUS).

This library is used by JetBrains internal products and services to send analytics events using the LION v4 wire protocol. It is the Go equivalent of the JVM implementation and is wire-compatible with it, producing identical JSON payloads and SHA-256 device hashes.

## Intended audience

This repository is maintained for JetBrains engineers integrating product analytics with FUS.

You may find the code useful for reference, but this is not a general-purpose public SDK:
- no public support is provided
- internal JetBrains assumptions may be embedded in the implementation
- compatibility is maintained for internal consumers, not for arbitrary third-party integrations

## Installation

> [!CAUTION]
> The module path is public, but this package is intended for internal JetBrains use only.

```bash
go get github.com/JetBrains/fus-reporting-api-go
```

## Usage

```go
import fus "github.com/JetBrains/fus-reporting-api-go"

// 1. Define your scheme in Go. The same value is used at runtime for client-side
//    validation and marshalled to schema.json for AP metadata registration.
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

// 2. Create a logger — validator is required, salt fetch must succeed.
logger, err := fus.NewLogger(fus.RecorderConfig{
    RecorderID:      "FUS",       // Recorder code assigned by Data Office
    RecorderVersion: 1,
    ProductCode:     "TCC",       // Product code registered in FUS
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

// 4. Flush — sends buffered events to FUS. Call on shutdown.
logger.Close() // flushes with 2s timeout
```

## Architecture

```text
Track()  ──▶  validator  ──▶  escaper  ──▶  fus_buffer.jsonl  ──▶  Flush()  ──▶  POST /fus/v5/send/
                                              (disk JSONL)         (HTTP)         (analytics server)
```

- **Track()** appends one JSON line to a buffer file. No network call.
- **Validator** (required): rewrites the event so only scheme-approved keys and values reach the wire. Unknown keys / unmatched values are replaced with sentinels (`validation.unmatched_rule`, `validation.undefined_rule`). Events whose group is registered but whose build or version falls outside declared ranges are dropped.
- **Escaper**: normalizes string fields for LION v4 wire safety — drops `'` `"`, replaces CR/LF/TAB and separator chars, non-ASCII runes → `?`.
- **Buffer**: persists across process restarts. Events survive crashes and network failures.
- **Flush()** reads the buffer, POSTs events in batches of 500, re-queues any unsent batch on failure.
- **Anonymization** uses `SHA256(salt + value) + "#C"`, matching the JVM implementation. The SDK fails to start when no salt can be fetched or loaded from cache.

## Client-side validation

Client-side validation is a hard requirement; `NewLogger` refuses to return without a configured validator. The validator is a port of the JVM `SensitiveDataValidator` and supports the rule vocabulary used by typical FUS schemes:

| Rule | Expression | Helper |
|---|---|---|
| Inline enum | `enum:a\|b\|c` | `fus.EnumExpr("a", "b", "c")` |
| Enum reference | `enum#name` | `fus.EnumRefExpr("name")` |
| Inline regexp | `regexp:<pattern>` | `fus.RegexpExpr(pattern)` |
| Regexp reference | `regexp#name` | `fus.RegexpRefExpr("name")` |
| Inline int range | `range:<from>..<to>` | `fus.RangeExpr(from, to)` |
| Range reference | `range#name` | `fus.RangeRefExpr("name")` |

References resolve against the scheme's top-level `Rules` block (`Enums` / `Regexps` / `Ranges`).

**Not ported** (unused by current consumers, add when needed): `util#` rules, expression rules (`abc.{enum:x}.foo`), `required:` / `default_value:` rules, dictionary rules beyond plain enum, `system_data` / `client_data` / `ids` validators, `anonymized_fields`, LION v3.

FUS system events (`registered`, `invoked`, `invocation.failed`, `validation.too_many_events`, `validation.too_many_events.alert`) and pre-existing validator sentinels pass through validation untouched.

### Behavior summary

| Situation | Outcome |
|---|---|
| Group not in scheme | event kept, `event.id` and `event.data` replaced with `validation.undefined_rule` |
| Group known, build or version out of range | event dropped entirely |
| Event ID not in group's whitelist | `event.id` replaced with `validation.unmatched_rule` |
| Data field key not in scheme | key/value replaced with `validation.undefined_rule` sentinel entry |
| Data field value fails its rule | key kept, value replaced with `validation.unmatched_rule` |

### Permissive validator for tests

For SDK-internal tests and early bring-up, `fus.NewPermissiveValidator()` returns a validator that passes every event unchanged. **Do not use in production** — the name and docstring call this out explicitly.

## Generating `schema.json`

The `Scheme` type is JSON-marshalable in the AP metadata repo format. Ship a tiny generator alongside your CLI:

```go
// cmd/gen-scheme/main.go
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

Wire it into `go generate`. The exact same `telemetry.Scheme` value is passed to `fus.NewValidator` at runtime, so there's no drift between Go source and registered JSON.

## Regions and staging

- `RegionAll` (default) → `resources.jetbrains.com/storage/fus/config/v4/<recorder>/<product>.json`
- `RegionCN` → `resources.jetbrains.com.cn/...`
- `FetchTestConfig(recorder, product)` → `test/<recorder>/<product>.json` for staging validation.

Config is cached on disk (`fus_config.json` in `DataDir`) for 10 minutes. On fetch failure the SDK falls back to a cached salt if one exists; if neither a fresh fetch nor a cached salt is available, `NewLogger` returns an error (fail-closed — never ship events with a predictable hash).

## Design constraints

- **Fail-closed on salt**: `NewLogger` refuses to start without a non-empty salt from the config server or cache.
- **Validator required**: `NewLogger` refuses to start without a configured validator.
- **10-field cap**: events with more than 10 `data` fields are silently dropped in `Track`. Matches the FUS analytics UI limit.
- **Wire compatibility**: produces byte-identical LION v4 JSON and SHA-256 device hashes to the JVM SDK, verified by `TestIntegrationFullFlow` against the JVM test fixtures.

## Support and compatibility

This library is maintained for JetBrains internal consumers.

External users should assume:
- no support guarantees
- no compatibility guarantees
- no commitment to semantic versioning for third-party use

Internal JetBrains teams should coordinate recorder setup, product registration, and event metadata registration with the relevant maintainers and Data Office processes.
