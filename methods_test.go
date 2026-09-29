package kvdb

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type failStore struct{}

func (f *failStore) ReadFile(filePath string) ([]byte, error)    { return nil, nil }
func (f *failStore) WriteFile(filePath string, data []byte) error { return errors.New("disk full") }
func (f *failStore) AppendFile(filePath string, data []byte) error {
	return errors.New("disk full")
}

func TestGet(t *testing.T) {
	store := newMockStore()
	db, _ := New("test.db", nil, store)
	db.Set("foo", "bar")

	t.Run("gets an existing key", func(t *testing.T) {
		val, err := db.Get("foo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "bar" {
			t.Errorf("expected value 'bar', got '%s'", val)
		}
	})

	t.Run("returns an error for a non-existent key", func(t *testing.T) {
		_, err := db.Get("baz")
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
	})
}

func TestSet(t *testing.T) {
	store := newMockStore()
	db, _ := New("test.db", nil, store)

	t.Run("sets a new key-value pair", func(t *testing.T) {
		err := db.Set("foo", "bar")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		val, _ := db.Get("foo")
		if val != "bar" {
			t.Errorf("expected value 'bar', got '%s'", val)
		}
	})

	t.Run("updates an existing key", func(t *testing.T) {
		err := db.Set("foo", "baz")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		val, _ := db.Get("foo")
		if val != "baz" {
			t.Errorf("expected value 'baz', got '%s'", val)
		}
	})
}

func TestLogger(t *testing.T) {
	store := newMockStore()
	var buf bytes.Buffer
	logger := func(args ...any) { fmt.Fprintln(&buf, args...) }
	db, _ := New("test.db", logger, store)

	// Successful operations should not log
	db.Set("foo", "bar")
	if buf.Len() != 0 {
		t.Errorf("expected no logs for successful operations, got '%s'", buf.String())
	}

	// Now simulate a failing store to ensure errors are logged
	fs := &failStore{}
	var buf2 bytes.Buffer
	logger2 := func(args ...any) { fmt.Fprintln(&buf2, args...) }
	db2, _ := New("test.db", logger2, fs)
	// test failing append (insert)
	_ = db2.Set("a", "b")
	if !strings.Contains(buf2.String(), "error appending") {
		t.Errorf("expected error log for failing append, got '%s'", buf2.String())
	}

	// test failing persist (update) — Set schedules debounce; Flush forces the write
	buf2.Reset()
	_ = db2.Set("a", "c")
	_ = db2.Flush()
	if !strings.Contains(buf2.String(), "error persisting") {
		t.Errorf("expected error log for failing persist, got '%s'", buf2.String())
	}
}

func TestPersist_DoesNotDropExternalKey(t *testing.T) {
	store := newMockStore()
	store.WriteFile("test.db", []byte("A=1\n"))
	db, _ := New("test.db", nil, store)

	// write EXTERNAL=1 into the store behind db's back
	store.WriteFile("test.db", []byte("A=1\nEXTERNAL=1\n"))

	_ = db.Set("A", "9")
	_ = db.Flush()

	data, _ := store.ReadFile("test.db")
	if !strings.Contains(string(data), "EXTERNAL=1") {
		t.Errorf("expected store to contain EXTERNAL=1, got %q", string(data))
	}
}

func TestPersist_DoesNotDropComments(t *testing.T) {
	store := newMockStore()
	store.WriteFile("test.db", []byte("# comment\nA=1\n"))
	db, _ := New("test.db", nil, store)

	_ = db.Set("A", "9")
	_ = db.Flush()

	data, _ := store.ReadFile("test.db")
	if !strings.Contains(string(data), "# comment") {
		t.Errorf("expected store to contain '# comment', got %q", string(data))
	}
}

func TestReload_AdoptsExternalChanges(t *testing.T) {
	store := newMockStore()
	store.WriteFile("test.db", []byte("A=1\n"))
	db, _ := New("test.db", nil, store)

	// External edit changes untouched key's value and adds B=2
	store.WriteFile("test.db", []byte("A=1\nB=2\n"))

	if err := db.Reload(); err != nil {
		t.Fatalf("unexpected error reloading: %v", err)
	}

	val, err := db.Get("B")
	if err != nil || val != "2" {
		t.Errorf("expected B=2 after reload, got val=%q err=%v", val, err)
	}
}

func TestReload_KeepsUnflushedLocalWrites(t *testing.T) {
	store := newMockStore()
	store.WriteFile("test.db", []byte("A=orig\n"))
	db, _ := New("test.db", nil, store)

	_ = db.Set("A", "local") // unflushed local write
	store.WriteFile("test.db", []byte("A=remote\n"))

	if err := db.Reload(); err != nil {
		t.Fatalf("unexpected error reloading: %v", err)
	}

	val, _ := db.Get("A")
	if val != "local" {
		t.Errorf("expected Get(\"A\") to be 'local', got %q", val)
	}
}

func TestReload_DropsExternalDeletionsOfUntouchedKeys(t *testing.T) {
	store := newMockStore()
	store.WriteFile("test.db", []byte("A=1\nB=2\n"))
	db, _ := New("test.db", nil, store)

	// External edit deletes B from disk
	store.WriteFile("test.db", []byte("A=1\n"))

	if err := db.Reload(); err != nil {
		t.Fatalf("unexpected error reloading: %v", err)
	}

	_, err := db.Get("B")
	if err == nil {
		t.Errorf("expected B to be deleted after reload, but it was found")
	}
}

func TestReload_AfterFlushAdoptsExternalEdits(t *testing.T) {
	store := newMockStore()
	store.WriteFile("test.db", []byte("A=orig\n"))
	db, _ := New("test.db", nil, store)

	_ = db.Set("A", "local")
	_ = db.Flush() // local write flushed to disk

	// External edit modifies A on disk after flush
	store.WriteFile("test.db", []byte("A=external\n"))

	if err := db.Reload(); err != nil {
		t.Fatalf("unexpected error reloading: %v", err)
	}

	val, _ := db.Get("A")
	if val != "external" {
		t.Errorf("expected Get(\"A\") to be 'external' after reload post-flush, got %q", val)
	}
}
