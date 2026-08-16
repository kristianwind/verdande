package store

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// NewID returns a UUIDv7: 48 bits of Unix milliseconds followed by randomness.
//
// Time-ordered ids matter more here than they look. SQLite stores rows in a B-tree
// keyed by the primary key, so random ids scatter inserts across the whole file and
// every new task dirties a different page. Sequential ids append. It also means
// `ORDER BY id` is creation order, which removes a column and an index from every
// table that would otherwise need one.
//
// Implemented directly rather than through a dependency: it is twenty lines, and the
// monotonic counter below is the only part with any subtlety.
func NewID() string {
	var b [16]byte

	ms, seq := nextTimestamp()
	// 48-bit big-endian millisecond timestamp.
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)

	// Version 7 in the high nibble of byte 6, then 12 bits of sequence. The
	// sequence is what keeps ids ordered *within* a millisecond — without it, two
	// tasks created in the same tick sort by their random bits, and a list created
	// by one fast paste comes back shuffled.
	b[6] = 0x70 | byte(seq>>8)
	b[7] = byte(seq)

	if _, err := rand.Read(b[8:]); err != nil {
		// crypto/rand does not fail on any supported platform; if it somehow
		// does, continuing would silently produce guessable ids.
		panic(fmt.Sprintf("store: read random for id: %v", err))
	}
	// Variant bits: 10xx in the top of byte 8.
	b[8] = (b[8] & 0x3f) | 0x80

	return hex.EncodeToString(b[:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:])
}

var idClock struct {
	sync.Mutex
	lastMS uint64
	seq    uint16
}

// nextTimestamp returns the current millisecond and a counter that increments for
// repeated calls inside the same one, so ids issued in a burst stay ordered.
func nextTimestamp() (uint64, uint16) {
	idClock.Lock()
	defer idClock.Unlock()

	ms := uint64(time.Now().UnixMilli())
	switch {
	case ms > idClock.lastMS:
		idClock.lastMS, idClock.seq = ms, 0
	case idClock.seq < 0xfff:
		idClock.seq++
	default:
		// More than 4096 ids in one millisecond: borrow from the next. The clock
		// value stays ahead of real time until it catches up, which keeps ids
		// unique and ordered rather than wrapping the counter.
		idClock.lastMS++
		idClock.seq = 0
		ms = idClock.lastMS
	}
	return ms, idClock.seq
}

// IDTime recovers the moment an id was generated. Useful in diagnostics, and the
// reason a created_at column can be trusted against its own row's id.
func IDTime(id string) (time.Time, bool) {
	clean := make([]byte, 0, 32)
	for i := 0; i < len(id); i++ {
		if id[i] != '-' {
			clean = append(clean, id[i])
		}
	}
	if len(clean) != 32 {
		return time.Time{}, false
	}
	decoded := make([]byte, 16)
	if _, err := hex.Decode(decoded, clean); err != nil {
		return time.Time{}, false
	}
	if decoded[6]>>4 != 7 {
		return time.Time{}, false
	}
	ms := uint64(binary.BigEndian.Uint16(decoded[0:2]))<<32 |
		uint64(binary.BigEndian.Uint32(decoded[2:6]))
	return time.UnixMilli(int64(ms)), true
}
