package kvdb

import (
	. "webtyp.com/fmt"
)

// lineKind classifies a line of the backing file.
const (
	kindOther = iota // comment, blank, or anything without '='
	kindPair
)

const (
	sepEquals  = "="
	sepNewline = "\n"
)

type fileLine struct {
	raw  string // the line exactly as read, without its trailing newline
	key  string // "" unless kind == kindPair
	kind int
}

// parseLines splits the backing file into classified lines, preserving order.
func parseLines(data []byte) []fileLine {
	if len(data) == 0 {
		return nil
	}
	str := string(data)
	if len(str) > 0 && str[len(str)-1] == '\n' {
		str = str[:len(str)-1]
	}
	lines := Convert(str).Split(sepNewline)
	res := make([]fileLine, 0, len(lines))
	for _, line := range lines {
		trimmed := Convert(line).TrimSpace().String()
		if len(trimmed) > 0 && trimmed[0] == '#' {
			res = append(res, fileLine{raw: line, kind: kindOther})
			continue
		}
		key, _ := splitOnFirstEquals(trimmed)
		if key != "" {
			res = append(res, fileLine{raw: line, key: key, kind: kindPair})
		} else {
			res = append(res, fileLine{raw: line, kind: kindOther})
		}
	}
	return res
}

// reconcile returns the bytes to write: every line of disk is preserved in
// place, pair lines whose key is in touched are re-emitted with the in-memory
// value, and touched keys absent from disk are appended in insertion order.
func reconcile(disk []byte, data []pair, touched map[string]bool) []byte {
	c := Convert()
	emittedOnDisk := make(map[string]bool)

	diskLines := parseLines(disk)
	for _, line := range diskLines {
		if line.kind == kindPair {
			if touched[line.key] && !emittedOnDisk[line.key] {
				emittedOnDisk[line.key] = true
				val := ""
				for _, p := range data {
					if p.Key == line.key {
						val = p.Value
						break
					}
				}
				c.Write(line.key)
				c.Write(sepEquals)
				c.Write(val)
				c.Write(sepNewline)
				continue
			}
			emittedOnDisk[line.key] = true
		}
		c.Write(line.raw)
		c.Write(sepNewline)
	}

	for _, p := range data {
		if touched[p.Key] && !emittedOnDisk[p.Key] {
			emittedOnDisk[p.Key] = true
			c.Write(p.Key)
			c.Write(sepEquals)
			c.Write(p.Value)
			c.Write(sepNewline)
		}
	}

	out := make([]byte, len(c.Bytes()))
	copy(out, c.Bytes())
	return out
}

// countPairs reports how many key=value lines data contains, so a successful
// write can update the instance's picture of the file without re-reading it.
func countPairs(data []byte) int {
	n := 0
	for _, line := range parseLines(data) {
		if line.kind == kindPair {
			n++
		}
	}
	return n
}
