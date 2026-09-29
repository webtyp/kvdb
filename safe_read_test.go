package kvdb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// ctrlStore is a Store test double whose reads can be forced to fail or to
// come back empty, simulating the pre-write read racing a truncating writer.
type ctrlStore struct {
	files     map[string][]byte
	failRead  bool
	emptyRead bool
}

func newCtrlStore() *ctrlStore {
	return &ctrlStore{files: make(map[string][]byte)}
}

func (s *ctrlStore) ReadFile(filePath string) ([]byte, error) {
	if s.failRead {
		return nil, errors.New("simulated read failure")
	}
	data, ok := s.files[filePath]
	if !ok {
		return nil, os.ErrNotExist
	}
	if s.emptyRead {
		return []byte{}, nil
	}
	return data, nil
}

func (s *ctrlStore) WriteFile(filePath string, data []byte) error {
	s.files[filePath] = data
	return nil
}

func (s *ctrlStore) AppendFile(filePath string, data []byte) error {
	s.files[filePath] = append(s.files[filePath], data...)
	return nil
}

func fileOf(t *testing.T, s *ctrlStore, path string) string {
	t.Helper()
	raw, ok := s.files[path]
	if !ok {
		t.Fatalf("expected file %q to exist in store", path)
	}
	return string(raw)
}

func TestRewriteAborted_ReadErrorWithKnownKeys(t *testing.T) {
	store := newCtrlStore()
	store.files["test.db"] = []byte("A=1\nB=2\n")
	var buf bytes.Buffer
	logger := func(args ...any) { fmt.Fprintln(&buf, args...) }
	db, _ := New("test.db", logger, store)

	_ = db.Set("A", "9")
	store.failRead = true
	err := db.Flush()

	if err == nil {
		t.Fatal("expected Flush to return an error when the pre-write read fails, got nil")
	}
	got := fileOf(t, store, "test.db")
	if !strings.Contains(got, "A=1") || !strings.Contains(got, "B=2") {
		t.Errorf("expected file untouched (A=1, B=2), got %q", got)
	}
	if strings.Contains(got, "A=9") {
		t.Errorf("aborted write must not reach the file, got %q", got)
	}
	if !strings.Contains(buf.String(), "test.db") {
		t.Errorf("expected log message to contain the path, got %q", buf.String())
	}
}

func TestRewriteAllowed_ReadErrorWithNoKnownKeys(t *testing.T) {
	store := newCtrlStore()
	db, _ := New("test.db", nil, store)

	if err := db.Set("A", "1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := db.Flush(); err != nil {
		t.Fatalf("first write to a missing file must not fail, got: %v", err)
	}
	if got := fileOf(t, store, "test.db"); !strings.Contains(got, "A=1") {
		t.Errorf("expected file to contain A=1, got %q", got)
	}
}

func TestRewriteAborted_EmptyReadWithKnownKeys(t *testing.T) {
	store := newCtrlStore()
	store.files["test.db"] = []byte("A=1\nB=2\n")
	db, _ := New("test.db", nil, store)

	_ = db.Set("A", "9")
	store.emptyRead = true
	err := db.Flush()

	if err == nil {
		t.Fatal("expected Flush to return an error on an empty read with known keys, got nil")
	}
	got := fileOf(t, store, "test.db")
	if !strings.Contains(got, "A=1") || !strings.Contains(got, "B=2") {
		t.Errorf("expected file untouched (A=1, B=2), got %q", got)
	}
}

func TestRewriteAllowed_EmptyReadWithNoKnownKeys(t *testing.T) {
	store := newCtrlStore()
	store.files["test.db"] = []byte{}
	db, _ := New("test.db", nil, store)

	if err := db.Set("A", "1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := db.Flush(); err != nil {
		t.Fatalf("write to a genuinely empty file must not fail, got: %v", err)
	}
	if got := fileOf(t, store, "test.db"); !strings.Contains(got, "A=1") {
		t.Errorf("expected file to contain A=1, got %q", got)
	}
}

func TestRewriteAborted_TouchedKeysSurvive(t *testing.T) {
	store := newCtrlStore()
	store.files["test.db"] = []byte("A=1\nB=2\n")
	db, _ := New("test.db", nil, store)

	_ = db.Set("A", "9")
	store.failRead = true
	if err := db.Flush(); err == nil {
		t.Fatal("expected first Flush to fail, got nil")
	}
	store.failRead = false
	if err := db.Flush(); err != nil {
		t.Fatalf("expected retry Flush to succeed, got: %v", err)
	}
	got := fileOf(t, store, "test.db")
	if !strings.Contains(got, "A=9") || !strings.Contains(got, "B=2") {
		t.Errorf("expected deferred write to land (A=9, B=2), got %q", got)
	}
}

func TestRewriteAborted_DiskKeyCountAfterSuccessfulWrite(t *testing.T) {
	store := newCtrlStore()
	// Start from nothing so diskKeyCount is 0: only a successful write can
	// teach the instance that the file has keys.
	db, _ := New("test.db", nil, store)

	// New keys take the append path; the update + Flush below is the first
	// full rewrite, after which the instance knows the file holds 2 keys.
	if err := db.Set("A", "1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := db.Set("B", "2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := db.Set("A", "9"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := db.Flush(); err != nil {
		t.Fatalf("unexpected error flushing: %v", err)
	}
	if got := fileOf(t, store, "test.db"); !strings.Contains(got, "A=9") || !strings.Contains(got, "B=2") {
		t.Fatalf("expected file to contain A=9 and B=2, got %q", got)
	}

	store.failRead = true
	_ = db.Set("B", "3")
	if err := db.Flush(); err == nil {
		t.Fatal("expected Flush to abort once the instance knows the file has keys, got nil")
	}
	got := fileOf(t, store, "test.db")
	if !strings.Contains(got, "A=9") || !strings.Contains(got, "B=2") {
		t.Errorf("expected file left intact (A=9, B=2), got %q", got)
	}
	if strings.Contains(got, "B=3") {
		t.Errorf("aborted write must not reach the file, got %q", got)
	}
}
