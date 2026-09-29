package kvdb

import (
	. "webtyp.com/fmt"
	. "webtyp.com/time"
)

const (
	msgErrPersisting = "error persisting:"
	msgErrAppending  = "error appending:"
	// msgRewriteAborted is logged instead of destroying the file. It names the
	// path because the reader's next question is always "which file?".
	msgRewriteAborted = "kvdb: refusing to rewrite (the file could not be read back, or came back empty when it is known to have keys); nothing was written to:"
)

// Keys returns every key currently stored, in insertion order.
func (t *TinyDB) Keys() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	keys := make([]string, 0, len(t.data))
	for _, p := range t.data {
		keys = append(keys, p.Key)
	}
	return keys
}

func (t *TinyDB) Get(key string) (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, p := range t.data {
		if p.Key == key {
			return p.Value, nil
		}
	}
	return "", Err("key not found: ", key)
}

func (t *TinyDB) Set(key, value string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.touched[key] = true

	// search if it exists
	for i, p := range t.data {
		if p.Key == key {
			t.data[i].Value = value
			return t.schedulePersist()
		}
	}

	// insert new
	newPair := pair{Key: key, Value: value}
	t.data = append(t.data, newPair)
	return t.append(newPair)
}

// schedulePersist either writes immediately (no debounce) or defers the write.
// Must be called with t.mu held.
func (t *TinyDB) schedulePersist() error {
	if t.debounceDelay == 0 {
		return t.persist()
	}
	t.dirty = true
	if t.debounceTimer == nil {
		t.debounceTimer = AfterFunc(t.debounceDelay, func() {
			// Snapshot data under lock, then write to disk outside the lock.
			// This keeps the lock window minimal so Get/Set calls are not
			// blocked during the (potentially slow) disk I/O.
			t.mu.Lock()
			if !t.dirty {
				t.debounceTimer = nil
				t.mu.Unlock()
				return
			}
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

			if err := t.store.WriteFile(t.name, data); err != nil {
				t.log(msgErrPersisting, err.Error())
				return
			}
			t.mu.Lock()
			t.diskKeyCount = newCount
			t.mu.Unlock()
		})
	}
	return nil
}

func (t *TinyDB) append(p pair) error {
	t.raw.Reset()
	t.raw.Write(p.Key)
	t.raw.Write("=")
	t.raw.Write(p.Value)
	t.raw.Write("\n")

	if err := t.store.AppendFile(t.name, t.raw.Bytes()); err != nil {
		// log only on error
		t.log(msgErrAppending, err.Error())
		return err
	}

	delete(t.touched, p.Key)
	return nil
}

func (t *TinyDB) persist() error {
	disk, safe := t.readDiskForRewrite()
	if !safe {
		t.log(msgRewriteAborted, t.name)
		// Keep the pending writes pending: Flush clears dirty before calling
		// persist, and the immediate-write path never sets it, so mark dirty
		// here to guarantee the next Set or Flush retries the write.
		t.dirty = true
		return Err(msgRewriteAborted, t.name)
	}
	data := reconcile(disk, t.data, t.touched)
	if err := t.store.WriteFile(t.name, data); err != nil {
		t.log(msgErrPersisting, err.Error())
		return err
	}
	t.touched = make(map[string]bool)
	t.diskKeyCount = countPairs(data)
	return nil
}
