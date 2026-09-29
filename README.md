# TinyDB
<img src="docs/img/badges.svg">

TinyDB is a TinyGo-compatible, minimal key–value store for string keys and values. It intentionally provides a very small API and a pluggable storage backend so it can be embedded into small or resource-constrained projects.

## Summary

- Purpose: store and retrieve string key/value pairs persistently with a tiny, dependency-free runtime.
- Target: TinyGo and small Go programs where avoiding heavy runtime features (JSON, reflection, fmt) is desirable.
- Persistence: delegated to a user-provided `Store` implementation.

## Key features

- Minimal public API: `Get`, `Set` and `Flush` operations. Optional `Reload()` method on `TinyDB`.
- Automatic write debounce (150ms) for updates to reduce I/O.
- Reconciliation guarantee: writes only change keys modified by this process. Comments, blank lines, key order, and unknown/external keys are preserved byte-for-byte.
- Keys and values are plain `string` types.
- Pluggable storage via the `Store` interface (file, memory, remote object store, etc.).
- Simple on-disk/text format: one `key=value` entry per line.
- TinyGo friendly: no use of reflection or encoding/json in the core.

## Quick start

1. Implement the `Store` interface for your persistence layer (file, in-memory, S3, etc.).
2. Create a database instance with `tinydb.New(name, logger, store)`.
3. Use `Get(key)` and `Set(key, value)` to read and write values.
4. Call `Flush()` before exiting the application to ensure all data is written to disk.
5. Use `Reload()` if external processes modify the backing file and you want to merge changes into memory without overwriting unflushed local writes.

Example minimal flow:

- Implement `Store`
- New DB: `db, err := tinydb.New("mydb.tdb", logger, store)`
- Set: `db.Set("foo", "bar")`
- Get: `val, err := db.Get("foo")`
- Flush: `db.Flush()`
- Reload: `err := db.Reload()`

## API contract (concise)

- Inputs: `key` and `value` are `string`.
- Outputs: `Get` returns `(string, error)`. `Set`, `Flush`, and `Reload` return `error`.
- Persistence: handled by the `Store` implementation; `Set` for updates is debounced by 150ms.
- Failure modes: I/O errors or backend errors are returned as `error` from the public methods.

Edge cases to consider:

- Missing key: `Get` returns `""` and a non-nil `error`.
- Backend I/O failure: propagated as an `error`.
- Values containing newlines or `=` characters: the canonical on-disk format is `key=value` per line; avoid storing raw newlines in values.

## Interfaces (example)

The public interfaces look like this:

```go
type KVStore interface {
    Get(key string) (string, error)
    Set(key, value string) error
    Flush() error
}

// Store is where the database file lives: any webtyp.com/files implementation that can
// read, write and append whole files (webtyp/opfs in the browser, a disk store on a server,
// webtyp.com/files/mem in tests). AppendFile is used when adding a new key/value pair, so
// an insert does not rewrite the whole file. A missing file is files.ErrNotExist.
type Store interface {
    files.ReadWriter // ReadFile(path) ([]byte, error) · WriteFile(path, data) error
    files.Appender   // AppendFile(path, data) error
}
```

Constructor example:

```go
db, err := tinydb.New("mydb.tdb", logger, store)
```

- `name` (string): logical database name; commonly a file path used by the `Store` implementation.
- `logger` (`tinydb.LoggerFunc`, signature `func(...any)`): optional logger function; may be a wrapper that writes to `os.Stdout` or `nil`.
- `store` (Store): required backend implementation.

## Minimal example (file-backed store)

```go
package main

import (
    "os"
    "webtyp.com/kvdb"
)

type FileStore struct{}

func (fs FileStore) ReadFile(path string) ([]byte, error) {
    return os.ReadFile(path)
}

func (fs FileStore) WriteFile(path string, data []byte) error {
    return os.WriteFile(path, data, 0644)
}

// AppendFile appends bytes to the end of the named file. This is used by
// tinydb when inserting new key/value pairs to avoid rewriting the whole
// store on every insert.
func (fs FileStore) AppendFile(path string, data []byte) error {
    f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
    if err != nil {
        return err
    }
    defer f.Close()
    _, err = f.Write(data)
    return err
}

func main() {
    // Simple LoggerFunc using only the built-in print/println helpers
    logger := func(args ...any) {
        for i, a := range args {
            if i > 0 {
                print(" ")
            }
            print(a)
        }
        println()
    }
    db, err := tinydb.New("settings.tdb", logger, FileStore{})
    if err != nil {
        panic(err)
    }

    if err := db.Set("username", "cesar"); err != nil {
        panic(err)
    }

    // Ensure changes are persisted before exiting
    if err := db.Flush(); err != nil {
        panic(err)
    }

    val, err := db.Get("theme")
    if err != nil {
        // key missing or other error
        println("error getting key:", err.Error())
        return
    }
    println("theme:", val)
}
```

## Storage format & Reconciliation

By default tinydb uses a simple text representation: one `key=value` per line.

Example file contents:

```
# Application configuration
username=cesar
theme=dark
window=1024x768
```

Notes:

- Avoid embedding newlines in values; the implementation expects single-line entries.
- If your values may contain `=` or newline characters, use an encoding strategy in your `Store` or pre-encode values before calling `Set`.
- TinyDB reconciles on-disk contents when persisting: comments, blank lines, and externally added keys or edits are preserved. Only keys explicitly written by `Set` in this process are updated.

## Testing suggestions

- Unit test the `Store` implementations (file, memory, mocks) independently.
- Add tests for basic Set/Get behaviour and for recovery after failures (simulate I/O errors).




## Benchmarks ⚡️

Run on: Linux (11th Gen Intel i7-11800H @ 2.30GHz)

The following micro-benchmark measures allocations and time for repeated `Set` operations.

| Test | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| BenchmarkSetAlloc-16 | 96,493 | 364 | 5 |

Quick notes:
- 🟢 ns/op — lower is better (time per operation).
- 🟢 B/op and allocs/op — lower is better (memory allocations per operation).
- 🟡 Result: reasonable for a small in-memory store; low allocations but non-negligible per-op cost.
- 💡 Suggestion: consider reusing buffers (buffer pools) to reduce allocations if you need maximum optimization.
- ⚠️ This benchmark uses an in-memory `memStore` (no disk I/O); file-backed implementations will be slower.


## Limitations

- Not a full database: tinydb is intended for small key/value needs with simple persistence.
- No indexing, transactions, concurrency locking, or binary values support out of the box.

## License

MIT
