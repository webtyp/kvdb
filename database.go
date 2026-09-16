package kvdb

import (
	"sync"

	. "webtyp.com/fmt"
	. "webtyp.com/time"
)

type pair struct {
	Key   string
	Value string
}

// splitOnFirstEquals splits a string on the first '=' character.
// Returns (key, value). If no '=' is found, returns ("", "").
func splitOnFirstEquals(s string) (key, value string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:]
		}
	}
	return "", ""
}

// LoggerFunc is a simple logger that accepts any values (like fmt.Println).
// Use a nil LoggerFunc when you want no-op logging; New will set a safe default.
type LoggerFunc func(...any)

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

	debounceDelay int   // milliseconds
	debounceTimer Timer // from webtyp.com/time
	dirty         bool
}

// defaultDebounce is the write delay applied automatically (milliseconds).
// Consecutive Set() calls within this window are coalesced into a single disk write.
const defaultDebounce = 150

// Flush writes any pending debounced state to disk immediately.
// Call before process exit when debounce is enabled.
func (t *TinyDB) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.dirty {
		return nil
	}
	if t.debounceTimer != nil {
		t.debounceTimer.Stop()
		t.debounceTimer = nil
	}
	t.dirty = false
	return t.persist()
}

// Reload re-reads the backing file and merges it into memory. Keys written by
// this process since the last flush keep their in-memory value; every other key
// takes the value on disk, and keys only present on disk are added.
func (t *TinyDB) Reload() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	raw, err := t.store.GetFile(t.name)
	if err != nil {
		return err
	}

	diskLines := parseLines(raw)
	diskPairs := make([]pair, 0)
	diskKeys := make(map[string]bool)

	for _, line := range diskLines {
		if line.kind == kindPair && !diskKeys[line.key] {
			diskKeys[line.key] = true
			_, val := splitOnFirstEquals(Convert(line.raw).TrimSpace().String())
			diskPairs = append(diskPairs, pair{Key: line.key, Value: val})
		}
	}

	// Rebuild t.data keeping touched keys with local value, adopting disk values for untouched keys,
	// and appending local-only touched keys.
	newData := make([]pair, 0, len(diskPairs)+len(t.data))
	seenInNewData := make(map[string]bool)

	for _, dp := range diskPairs {
		seenInNewData[dp.Key] = true
		if t.touched[dp.Key] {
			// keep local value
			localVal := dp.Value
			for _, p := range t.data {
				if p.Key == dp.Key {
					localVal = p.Value
					break
				}
			}
			newData = append(newData, pair{Key: dp.Key, Value: localVal})
		} else {
			// adopt disk value
			newData = append(newData, pair{Key: dp.Key, Value: dp.Value})
		}
	}

	// Append keys that exist locally in t.data but NOT on disk ONLY IF they are touched
	for _, p := range t.data {
		if !seenInNewData[p.Key] && t.touched[p.Key] {
			seenInNewData[p.Key] = true
			newData = append(newData, p)
		}
	}

	t.data = newData
	t.diskKeyCount = len(diskPairs)
	return nil
}

// New creates or loads a database
func New(name string, log LoggerFunc, store Store) (*TinyDB, error) {
	if log == nil {
		log = func(...any) {}
	}

	db := &TinyDB{
		name:          name,
		data:          make([]pair, 0),
		log:           log,
		store:         store,
		touched:       make(map[string]bool),
		raw:           Convert(),
		debounceDelay: defaultDebounce,
	}

	// try to load DB from Store
	raw, err := store.GetFile(name)
	if err == nil && len(raw) > 0 {
		lines := Convert(string(raw)).Split("\n")
		for _, line := range lines {
			if Convert(line).TrimSpace().String() == "" {
				continue
			}
			// Split only on the first '=' to handle values containing '='
			// (e.g. POSTGRES_DSN with query parameters like ?sslmode=disable).
			trimmed := Convert(line).TrimSpace().String()
			key, val := splitOnFirstEquals(trimmed)
			if key != "" {
				db.data = append(db.data, pair{
					Key:   key,
					Value: val,
				})
			}
		}
	}

	db.diskKeyCount = len(db.data)

	return db, nil
}
