package kvdb

import (
	"os"
	"testing"
)

// mockStore is a mock implementation of the Store interface for testing.
type mockStore struct {
	files map[string][]byte
}

func newMockStore() *mockStore {
	return &mockStore{
		files: make(map[string][]byte),
	}
}

func (m *mockStore) ReadFile(filePath string) ([]byte, error) {
	data, ok := m.files[filePath]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m *mockStore) WriteFile(filePath string, data []byte) error {
	m.files[filePath] = data
	return nil
}

func (m *mockStore) AppendFile(filePath string, data []byte) error {
	m.files[filePath] = append(m.files[filePath], data...)
	return nil
}

func TestNew(t *testing.T) {
	t.Run("creates a new database if one does not exist", func(t *testing.T) {
		store := newMockStore()
		db, err := New("test.db", nil, store)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if db == nil {
			t.Fatal("expected db to be non-nil")
		}
	})

	t.Run("loads an existing database from the store", func(t *testing.T) {
		store := newMockStore()
		store.WriteFile("test.db", []byte("foo=bar\nbaz=qux"))
		db, err := New("test.db", nil, store)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		val, err := db.Get("foo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "bar" {
			t.Errorf("expected value 'bar', got '%s'", val)
		}

		val, err = db.Get("baz")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "qux" {
			t.Errorf("expected value 'qux', got '%s'", val)
		}
	})

	t.Run("handles empty or malformed lines when loading", func(t *testing.T) {
		store := newMockStore()
		store.WriteFile("test.db", []byte("foo=bar\n\nmalformed\nbaz=qux"))
		db, err := New("test.db", nil, store)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		val, err := db.Get("foo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "bar" {
			t.Errorf("expected value 'bar', got '%s'", val)
		}

		val, err = db.Get("baz")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "qux" {
			t.Errorf("expected value 'qux', got '%s'", val)
		}

		_, err = db.Get("malformed")
		if err == nil {
			t.Error("expected error for malformed key, got nil")
		}
	})

	// Reproduces the bug where starting webtyp silently deletes externally
	// set .env values that contain more than one '=' (e.g. POSTGRES_DSN
	// connection strings with query parameters like "?sslmode=disable").
	//
	// Root cause: New() splits each line on every '=' via Split("=") and only
	// keeps the pair when len(kv) == 2 (database.go). A DSN value produces
	// len(kv) > 2, so the whole line is dropped from memory on load. Any
	// later Set() call for an unrelated key then persists only the in-memory
	// data back to disk (full overwrite), permanently erasing the DSN line.
	t.Run("does not delete external env values containing multiple '=' (e.g. POSTGRES_DSN)", func(t *testing.T) {
		store := newMockStore()
		const dsn = "postgres://user:pass@host:5432/db?sslmode=disable&application_name=webtyp"
		store.WriteFile("test.db", []byte("POSTGRES_DSN="+dsn+"\ndev_mode=false"))

		db, err := New("test.db", nil, store)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		val, err := db.Get("POSTGRES_DSN")
		if err != nil {
			t.Fatalf("POSTGRES_DSN was dropped on load: %v", err)
		}
		if val != dsn {
			t.Errorf("expected POSTGRES_DSN %q, got %q", dsn, val)
		}

		// Simulate webtyp startup writing an unrelated key (e.g. dev_mode),
		// which triggers a full-file persist of in-memory data.
		if err := db.Set("dev_mode", "true"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := db.Flush(); err != nil {
			t.Fatalf("unexpected error flushing: %v", err)
		}

		// Reload straight from the store to confirm what actually landed on disk.
		reloaded, err := New("test.db", nil, store)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		val, err = reloaded.Get("POSTGRES_DSN")
		if err != nil {
			t.Fatalf("POSTGRES_DSN was deleted from disk after Set/Flush: %v", err)
		}
		if val != dsn {
			t.Errorf("expected POSTGRES_DSN %q after persist, got %q", dsn, val)
		}
	})
}
