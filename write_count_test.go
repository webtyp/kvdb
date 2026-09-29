package kvdb

import (
	"sync"
	"testing"
	"time"
)

// countingStore wraps mockStore and counts how many times WriteFile is called.
// The mutex protects setFileCount against the race between the debounce timer
// goroutine (writer) and the test goroutine (reader).
type countingStore struct {
	mockStore
	mu           sync.Mutex
	setFileCount int
}

func (c *countingStore) WriteFile(path string, data []byte) error {
	c.mu.Lock()
	c.setFileCount++
	c.mu.Unlock()
	return c.mockStore.WriteFile(path, data)
}

func (c *countingStore) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.setFileCount
}

func (c *countingStore) resetCount() {
	c.mu.Lock()
	c.setFileCount = 0
	c.mu.Unlock()
}

// TestDebounceCoalescesRapidWrites verifies that with debounce enabled, N rapid
// Set() calls produce only 1 disk write instead of N.
func TestDebounceCoalescesRapidWrites(t *testing.T) {
	cs := &countingStore{mockStore: *newMockStore()}
	cs.WriteFile("test.env", []byte("browser_position=0,0\nbrowser_size=900,700\ndev_mode=true\n"))
	db, _ := New("test.env", nil, cs)
	cs.resetCount()

	// 3 rapid Set() calls — same pattern as devbrowser.SaveGeometry per tick.
	db.Set("browser_position", "300,400")
	db.Set("browser_size", "1024,768")
	db.Set("browser_position", "300,400")

	// In-memory values must be immediately correct (no wait needed).
	pos, _ := db.Get("browser_position")
	if pos != "300,400" {
		t.Errorf("in-memory position: got %q, want %q", pos, "300,400")
	}

	// No disk write should have happened yet (debounce window still open).
	if cs.count() != 0 {
		t.Errorf("premature write: got %d writes before debounce fired, want 0", cs.count())
	}

	// Wait for the default debounce (150ms) to flush.
	time.Sleep(250 * time.Millisecond)

	if cs.count() != 1 {
		t.Errorf("debounce: got %d disk writes for 3 rapid Set() calls, want 1", cs.count())
	}

	// Values must be persisted after flush.
	size, _ := db.Get("browser_size")
	if size != "1024,768" {
		t.Errorf("browser_size after flush: got %q, want %q", size, "1024,768")
	}
}

// TestFlushWritesPendingState verifies that Flush() forces an immediate disk
// write even if the debounce timer has not fired yet.
func TestFlushWritesPendingState(t *testing.T) {
	cs := &countingStore{mockStore: *newMockStore()}
	cs.WriteFile("test.env", []byte("browser_position=0,0\nbrowser_size=900,700\n"))
	db, _ := New("test.env", nil, cs)
	cs.resetCount()

	db.debounceDelay = 5000 // 5 seconds in milliseconds; override for test: long delay so timer won't fire

	db.Set("browser_position", "500,200")
	db.Set("browser_size", "1920,1080")

	if cs.count() != 0 {
		t.Errorf("unexpected write before Flush: %d", cs.count())
	}

	if err := db.Flush(); err != nil {
		t.Fatalf("Flush() error: %v", err)
	}

	if cs.count() != 1 {
		t.Errorf("Flush(): got %d writes, want 1", cs.count())
	}

	pos, _ := db.Get("browser_position")
	if pos != "500,200" {
		t.Errorf("position after Flush: got %q, want %q", pos, "500,200")
	}
}

// TestRapidSetsPreserveAllValues verifies that all values are correctly
// persisted after rapid consecutive Set() calls (with default debounce).
func TestRapidSetsPreserveAllValues(t *testing.T) {
	cs := &countingStore{mockStore: *newMockStore()}
	cs.WriteFile("test.env", []byte("browser_position=0,0\nbrowser_size=900,700\ndev_mode=true\n"))
	db, _ := New("test.env", nil, cs)
	cs.resetCount()

	db.Set("browser_position", "300,400")
	db.Set("browser_size", "1024,768")
	db.Set("browser_position", "300,400")

	pos, err := db.Get("browser_position")
	if err != nil || pos != "300,400" {
		t.Errorf("browser_position: got %q (err %v), want %q", pos, err, "300,400")
	}

	size, err := db.Get("browser_size")
	if err != nil || size != "1024,768" {
		t.Errorf("browser_size: got %q (err %v), want %q", size, err, "1024,768")
	}

	// Ensure everything is persisted
	if err := db.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if cs.count() != 1 {
		t.Errorf("expected 1 disk write (coalesced), got %d", cs.count())
	}
}
