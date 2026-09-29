package kvdb

import (
	"reflect"
	"testing"
)

func TestKeysEmptyStore(t *testing.T) {
	store := newMockStore()
	db, _ := New("test.db", nil, store)
	keys := db.Keys()
	if keys == nil {
		t.Fatalf("Keys() returned nil, want non-nil empty slice")
	}
	if len(keys) != 0 {
		t.Fatalf("Keys() = %v, want []", keys)
	}
}

func TestKeysReturnsInsertionOrder(t *testing.T) {
	store := newMockStore()
	db, _ := New("test.db", nil, store)
	db.Set("A", "1")
	db.Set("B", "2")
	db.Set("C", "3")
	keys := db.Keys()
	want := []string{"A", "B", "C"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("Keys() = %v, want %v", keys, want)
	}
}

func TestKeysReflectsLoadedFile(t *testing.T) {
	store := newMockStore()
	store.WriteFile("test.db", []byte("X=1\nY=2\n"))
	db, _ := New("test.db", nil, store)
	keys := db.Keys()
	want := []string{"X", "Y"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("Keys() = %v, want %v", keys, want)
	}
}

func TestKeysAfterOverwrite(t *testing.T) {
	store := newMockStore()
	db, _ := New("test.db", nil, store)
	db.Set("A", "1")
	db.Set("A", "2")
	keys := db.Keys()
	if len(keys) != 1 {
		t.Fatalf("Keys() length = %d, want 1", len(keys))
	}
	if keys[0] != "A" {
		t.Fatalf("Keys()[0] = %q, want \"A\"", keys[0])
	}
}
