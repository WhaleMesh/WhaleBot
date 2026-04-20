package logs

import (
	"sync"
	"time"
)

type Entry struct {
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type Ring struct {
	mu      sync.RWMutex
	entries []Entry
	cap     int
	next    int
	full    bool
}

func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = 200
	}
	return &Ring{entries: make([]Entry, capacity), cap: capacity}
}

func (r *Ring) Append(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	r.entries[r.next] = e
	r.next = (r.next + 1) % r.cap
	if r.next == 0 {
		r.full = true
	}
}

func (r *Ring) Recent() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Entry
	if r.full {
		out = append(out, r.entries[r.next:]...)
		out = append(out, r.entries[:r.next]...)
	} else {
		out = append(out, r.entries[:r.next]...)
	}
	return out
}
