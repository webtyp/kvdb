---
PLAN: "fix!: never rewrite the backing file from a read that failed or came back empty"
EXECUTOR: jules
REVIEWER: none
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.

# Plan — a bad read must abort the write, not silently truncate the file

## Why — this destroys user data today, reproducibly

`kvdb` is the store behind every WebTyp project's `.env`. A previous fix
(`6da7630 fix: never clobber external edits to the backing file`) made
`reconcile` preserve every line it is *given*. That fix is correct and stays.
The hole is one level up: **nothing checks that what `reconcile` is given is
actually the file.**

Both write paths read the file first and throw the error away:

`methods.go`, inside the debounce timer in `schedulePersist`:

```go
disk, _ := t.store.GetFile(t.name)
data := reconcile(disk, t.data, t.touched)
```

`methods.go`, in `persist`:

```go
func (t *TinyDB) persist() error {
	disk, _ := t.store.GetFile(t.name)
	data := reconcile(disk, t.data, t.touched)
	...
}
```

When that read fails, `disk` is `nil`. `reconcile` receives `nil`,
`parseLines(nil)` returns `nil` (`reconcile.go`, `if len(data) == 0 { return nil }`),
so there are no disk lines to preserve — and the output becomes **only the keys
in `touched`**. The store then writes that over the real file.

### Observed in production

A WebTyp project's `.env` containing a database connection string, five browser
settings and two framework keys was reduced to exactly:

```
dev_mode=true
WEBTYP_LAST_UPDATE_CHECK=2026-09-15T13:04:54-03:00
```

— precisely the two keys the writing process had touched. The project could not
start: its `DATABASE_URL` was gone. This is not hypothetical and it is not rare;
it recurred twice within one hour of debugging.

### Reproduced

This test fails today (the file is destroyed) and must pass after this plan:

```go
// store.GetFile returns an error exactly when the debounced write lands.
store := &failingReadStore{}
db, _ := New(path, nil, store)
db.Set("dev_mode", "true")
store.failRead = true
db.Flush()
// today: the file contains only "dev_mode=true"
// required: the file is untouched, and Flush reports the error
```

### The second trigger: an empty read that is not an error

`os.WriteFile` truncates before it writes. A concurrent reader — a second
`TinyDB` on the same path, or another process — can read that file mid-write and
get **zero bytes with a nil error**. That is indistinguishable from a
legitimately empty file by the return values alone, and it takes the same
destructive path. `kvdb` must therefore also refuse to treat "empty" as
authoritative when it has evidence the file was not empty.

## Anti-footguns — read before touching any file

1. **`kvdb` compiles for WASM.** It imports only `webtyp.com/fmt` and
   `webtyp.com/time`. Do **NOT** add `os`, `errors`, `strings`, `fmt` or any
   other standard-library import to solve this. The checks below are pure
   logic and need none. In particular, do NOT reach for `os.IsNotExist`.
2. **Do not change the `Store` interface** (`interfaces.go`). Its three methods
   are implemented outside this repo (`webtyp.com/app`'s `FileStore` and
   `MemoryStore`); adding a method breaks them. Everything here is solvable
   inside `TinyDB`.
3. **Do not change `reconcile`** (`reconcile.go`). It is correct: given the real
   file it preserves every line. The defect is in what it is handed.
4. **A missing file must still work.** A brand-new project has no `.env`, and
   `Set` must create it. "Read returned an error" cannot become a hard failure
   for that case — see `knownDiskKeys` below, which is what separates the two.

## The change

### 1. Track what this instance has seen on disk — `database.go`

Add one field to `TinyDB`:

```go
type TinyDB struct {
	name    string
	data    []pair
	log     LoggerFunc
	store   Store
	touched map[string]bool

	// diskKeyCount is how many key=value lines this instance last saw in the
	// backing file. It is the evidence used to reject a pre-write read that
	// came back emptier than the file is known to be — the signature of a read
	// that raced a truncating write, which returns zero bytes and a nil error
	// and is otherwise indistinguishable from an empty file.
	diskKeyCount int

	raw *Conv
	mu  sync.RWMutex

	debounceDelay int
	debounceTimer Timer
	dirty         bool
}
```

Set it in exactly two places, both of which already parse the file:

- `New` — after the load loop, `db.diskKeyCount = len(db.data)`.
- `Reload` — after `diskPairs` is built, `t.diskKeyCount = len(diskPairs)`.

### 2. One guarded read, used by both write paths — new file `safe_read.go`

```go
package kvdb

// readDiskForRewrite returns the current contents of the backing file, and
// whether it is safe to rewrite the file from them.
//
// A full rewrite reconstructs the file from what this read returns, so a read
// that is wrong by omission deletes data. Two cases are unsafe:
//
//   - the read failed, and this instance has previously seen keys in the file:
//     the file exists and has content we cannot currently see. Writing now
//     would replace it with whatever this process happens to hold.
//   - the read succeeded but returned nothing, and this instance has previously
//     seen keys in the file: almost certainly a read that landed inside another
//     writer's truncate window. A file does not empty itself.
//
// A read that fails or is empty when no keys were ever seen is the normal
// first-write case for a project that has no file yet, and is safe.
func (t *TinyDB) readDiskForRewrite() (disk []byte, safe bool) {
	raw, err := t.store.GetFile(t.name)
	if err != nil {
		return nil, t.diskKeyCount == 0
	}
	if len(raw) == 0 && t.diskKeyCount > 0 {
		return nil, false
	}
	return raw, true
}
```

### 3. Use it in `persist` — `methods.go`

```go
func (t *TinyDB) persist() error {
	disk, safe := t.readDiskForRewrite()
	if !safe {
		t.log(msgRewriteAborted, t.name)
		return Err(msgRewriteAborted, t.name)
	}
	data := reconcile(disk, t.data, t.touched)
	if err := t.store.SetFile(t.name, data); err != nil {
		t.log(msgErrPersisting, err.Error())
		return err
	}
	t.touched = make(map[string]bool)
	t.diskKeyCount = countPairs(data)
	return nil
}
```

**`t.touched` is NOT cleared on the abort path.** The pending writes stay
pending, so the next successful persist still carries them. Clearing them would
turn a deferred write into a lost one.

### 4. Use it in the debounce timer — `methods.go`

Inside the `AfterFunc` closure in `schedulePersist`, replace:

```go
		disk, _ := t.store.GetFile(t.name)
		data := reconcile(disk, t.data, t.touched)
		t.dirty = false
		t.debounceTimer = nil
		t.touched = make(map[string]bool)
		t.mu.Unlock()

		if err := t.store.SetFile(t.name, data); err != nil {
			t.log(msgErrPersisting, err.Error())
		}
```

with:

```go
		disk, safe := t.readDiskForRewrite()
		if !safe {
			// Keep dirty and touched intact: the data is still unwritten, and
			// the next Set or Flush must still try to write it.
			t.debounceTimer = nil
			t.mu.Unlock()
			t.log(msgRewriteAborted, t.name)
			return
		}
		data := reconcile(disk, t.data, t.touched)
		t.dirty = false
		t.debounceTimer = nil
		t.touched = make(map[string]bool)
		newCount := countPairs(data)
		t.mu.Unlock()

		if err := t.store.SetFile(t.name, data); err != nil {
			t.log(msgErrPersisting, err.Error())
			return
		}
		t.mu.Lock()
		t.diskKeyCount = newCount
		t.mu.Unlock()
```

### 5. `countPairs` — add to `reconcile.go`

```go
// countPairs reports how many key=value lines data contains, so a successful
// write can update the instance's picture of the file without re-reading it.
func countPairs(data []byte) int {
	n := 0
	for _, line := range parseLines(data) {
		if line.kind == kindPair {
			n++
		}
	}
	return n
}
```

### 6. The message — `methods.go`, next to the existing ones

```go
const (
	msgErrPersisting = "error persisting:"
	msgErrAppending  = "error appending:"
	// msgRewriteAborted is logged instead of destroying the file. It names the
	// path because the reader's next question is always "which file?".
	msgRewriteAborted = "kvdb: refusing to rewrite (the file could not be read back, or came back empty when it is known to have keys); nothing was written to:"
)
```

Use `msgRewriteAborted` as a typed constant everywhere; **no string literals in
logic.**

## Tests — `safe_read_test.go` (new file)

The package's existing tests use a `Store` test double; follow whatever
`database_test.go` and `reconcile_test.go` already do rather than inventing a
new style.

1. **Read error with known keys → file untouched.** Seed a file with
   `A=1\nB=2\n`, `New`, `Set("A","9")`, make the store's `GetFile` return an
   error, `Flush()`. Assert: `Flush` returns a non-nil error, the file on the
   store still contains both `A` and `B`, and the logger was called once with a
   message containing the path.
2. **Read error with no known keys → normal first write.** Empty store,
   `New` on a path that does not exist, `Set("A","1")`, `Flush()`. Assert no
   error and the file now contains `A=1`. (This is the new-project path and must
   not regress.)
3. **Empty read with known keys → file untouched.** Seed `A=1\nB=2\n`, `New`,
   `Set("A","9")`, make `GetFile` return `([]byte{}, nil)`, `Flush()`. Assert the
   file still has both keys and `Flush` returned an error.
4. **Empty read with no known keys → write proceeds.** A genuinely empty file
   is not a failure: `New` on an empty file, `Set("A","1")`, `Flush()`, assert
   `A=1` is written.
5. **Touched keys survive an aborted write.** Seed `A=1\nB=2\n`, `Set("A","9")`,
   fail the read, `Flush()` (error), restore the read, `Flush()` again. Assert
   the file now contains `A=9` **and** `B=2` — the deferred write was not lost.
6. **`diskKeyCount` after a successful write.** Seed `A=1\n`, `Set("B","2")`,
   `Flush()`, then fail the read and `Set("C","3")` + `Flush()`. Assert the
   second `Flush` aborts (the instance now knows the file has 2 keys), leaving
   `A` and `B` intact.

## Acceptance

- `go test ./...` passes.
- `grep -n "GetFile" methods.go` → **no match**. Both write paths go through
  `readDiskForRewrite`; no write path reads the file directly any more.
- `grep -rn "disk, _ :=" .` → **no match**.
- No new import appears in any non-test file:
  `git diff -- '*.go' ':(exclude)*_test.go' | grep '^+.*"os"'` → empty.
- `interfaces.go` is unchanged: `git diff --stat interfaces.go` → empty.
- `reconcile` itself is unchanged apart from the appended `countPairs`:
  `func reconcile` and `func parseLines` bodies identical to `main`.

## Stages

| Stage | Files | Done when |
|---|---|---|
| 1 | `database.go` | `diskKeyCount` field added and set in `New` and `Reload` |
| 2 | `reconcile.go` | `countPairs` added; `reconcile`/`parseLines` untouched |
| 3 | `safe_read.go` (new) | `readDiskForRewrite` implemented exactly as specified |
| 4 | `methods.go` | `msgRewriteAborted` constant added; `persist` and the debounce closure both go through `readDiskForRewrite`; `touched` preserved on abort |
| 5 | `safe_read_test.go` (new) | all six tests above pass |

## Follow-up owned by other repos — do NOT attempt here

The truncate window that produces the "empty read, nil error" case comes from
`os.WriteFile` in the `Store` implementation, which lives in
`https://github.com/webtyp/app` (`store.go`, `FileStore.SetFile`). Making that
write atomic (write a temp file, then rename) removes the window at its source.
That is tracked in that repo's own plan. This plan makes `kvdb` safe regardless
of which `Store` it is handed, which is the property that actually matters.
