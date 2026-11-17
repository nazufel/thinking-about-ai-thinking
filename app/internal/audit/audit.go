// Package audit keeps a trail of the decisions gatekeeper has made.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Entry records one decision.
type Entry struct {
	Time   time.Time `json:"time"`
	Tenant string    `json:"tenant"`
	Image  string    `json:"image"`
	Allow  bool      `json:"allow"`
	Reason string    `json:"reason"`
}

// Log holds the most recent entries in memory and, when a file is
// configured, appends every entry to it as a line of JSON.
type Log struct {
	mu      sync.Mutex
	entries []Entry
	max     int
	file    *os.File
}

// New returns a Log that keeps at most max entries in memory. If path is not
// empty, entries are also appended to that file.
func New(path string, max int) (*Log, error) {
	if max <= 0 {
		max = 1000
	}
	l := &Log{max: max}
	if path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open audit file: %w", err)
		}
		l.file = f
	}
	return l, nil
}

// Record adds an entry to the trail.
func (l *Log) Record(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
	if len(l.entries) > l.max {
		l.entries = l.entries[len(l.entries)-l.max:]
	}
	if l.file != nil {
		if line, err := json.Marshal(e); err == nil {
			l.file.Write(append(line, '\n'))
		}
	}
}

// ForTenant returns up to limit of a tenant's entries, newest first.
func (l *Log) ForTenant(tenant string, limit int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Entry{}
	for i := len(l.entries) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if l.entries[i].Tenant == tenant {
			out = append(out, l.entries[i])
		}
	}
	return out
}

// Close releases the audit file, if any.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
