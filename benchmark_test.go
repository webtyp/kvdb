package kvdb

import (
	"testing"

	. "webtyp.com/fmt"
)

// memStore is a lightweight in-memory Store used only for benchmarks.
type memStore struct {
	data []byte
}

func (m *memStore) GetFile(path string) ([]byte, error) { return m.data, nil }
func (m *memStore) SetFile(path string, data []byte) error {
	m.data = append([]byte(nil), data...)
	return nil
}

func (m *memStore) AddToFile(path string, data []byte) error {
	m.data = append(m.data, data...)
	return nil
}

// BenchmarkSetAlloc measures allocations when performing many Set operations.
// It reports allocations per operation and total time.
func BenchmarkSetAlloc(b *testing.B) {
	m := &memStore{}
	// Use nil logger to avoid logging overhead
	db, _ := New("bench.db", nil, m)

	b.ReportAllocs()

	keyBuild := Convert("key")
	valueBuild := Convert("value-for-")

	for i := 0; b.Loop(); i++ {
		key := keyBuild.Write(i)
		val := valueBuild.Write(i)
		if err := db.Set(key.String(), val.String()); err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
		keyBuild.Reset()
		valueBuild.Reset()
	}
}
