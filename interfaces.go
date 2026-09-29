package kvdb

import "webtyp.com/files"

// Store is where the database file lives: any webtyp.com/files implementation that can read,
// write and append whole files (webtyp/opfs in a browser Worker, a disk store on a server,
// files/mem in tests). A missing file is files.ErrNotExist, the normal first-run case.
type Store interface {
	files.ReadWriter
	files.Appender
}

// KVStore defines the minimum API
type KVStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
	// Keys returns every key currently stored, in insertion order. Empty
	// store returns an empty (non-nil) slice, never nil.
	Keys() []string
}
