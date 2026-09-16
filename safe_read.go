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
