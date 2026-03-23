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

// Create a logger for your product.
logger, err := fus.NewLogger(fus.RecorderConfig{
    RecorderID:      "FUS",       // Recorder code assigned by Data Office
    RecorderVersion: 1,
    ProductCode:     "TCC",       // Product code registered in FUS
    BuildVersion:    "0.5.0",
    DataDir:         "~/.config/myproduct/",
})

// Track events — appends to disk, sub-millisecond, never blocks.
logger.Track(
    fus.EventGroup{ID: "cli.command", Version: 1, State: false},
    "executed",
    map[string]any{"command": "build", "duration_ms": 1234},
)

// Flush — sends buffered events to FUS. Call on shutdown.
logger.Close() // flushes with 2s timeout
```

## Architecture

```text
Track()  ──▶  fus_buffer.jsonl  ──▶  Flush()  ──▶  POST /fus/v5/send/
 (disk)        (JSONL file)          (HTTP)         (analytics server)
```

- **Track()** appends one JSON line to a buffer file. No network call.
- **Flush()** reads the buffer, POSTs events in batches of 500, clears on success.
- **Buffer** persists across process restarts. Events survive crashes and network failures.
- **Anonymization** uses `SHA256(salt + value) + "#C"`, matching the JVM implementation.

## Support and compatibility

This library is maintained for JetBrains internal consumers.

External users should assume:
- no support guarantees
- no compatibility guarantees
- no commitment to semantic versioning for third-party use

Internal JetBrains teams should coordinate recorder setup, product registration, and event metadata registration with the relevant maintainers and Data Office processes.
