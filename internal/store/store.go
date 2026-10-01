// Package store holds the connections the plugin manages. It is the source of
// truth: herdr keeps only the ones that are currently active, because the
// plugin uses no herdr state beyond `machine add` and `machine remove`.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// ErrNotFound is returned for an unknown connection id.
var ErrNotFound = errors.New("store: no such connection")

// Connection is one SSH connection the user manages through the plugin.
// ProfileID is the herdr endpoint id while the connection is active, and empty
// once it has been removed from herdr.
type Connection struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Target    string    `json:"target"`
	Session   string    `json:"session,omitempty"`
	ProfileID string    `json:"profile_id,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// fileVersion is the layout of the connections file. A file from a newer
// version is refused rather than silently reinterpreted.
const fileVersion = 1

// Store is the connections file. Every mutation rewrites it through a
// temporary file that is flushed, then renamed, so neither a crash nor a power
// loss mid-write can leave a truncated file in place.
type Store struct {
	path string

	mu    sync.Mutex
	conns []Connection
}

type file struct {
	Version     int          `json:"version"`
	Connections []Connection `json:"connections"`
}

// Open reads path, treating a missing file as an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var decoded file
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("store: %s: %w", path, err)
	}
	if decoded.Version > fileVersion {
		return nil, fmt.Errorf("store: %s: version %d was written by a newer plugin", path, decoded.Version)
	}
	s.conns = decoded.Connections
	return s, nil
}

// List returns the connections in insertion order.
func (s *Store) List() []Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Connection(nil), s.conns...)
}

// Get returns one connection.
func (s *Store) Get(id string) (Connection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		if c.ID == id {
			return c, true
		}
	}
	return Connection{}, false
}

// Put inserts or updates a connection, assigning an id to a new one.
func (s *Store) Put(c Connection) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := slices.Clone(s.conns)
	c.Updated = time.Now().UTC()
	if c.ID == "" {
		c.ID = newID()
	}
	index := slices.IndexFunc(next, func(old Connection) bool { return old.ID == c.ID })
	if index < 0 {
		c.Created = c.Updated
		next = append(next, c)
	} else {
		c.Created = next[index].Created
		next[index] = c
	}
	if err := s.save(next); err != nil {
		return Connection{}, err
	}
	return c, nil
}

// Restore replaces an existing record exactly, including its timestamps, when
// the operation that required a save could not be queued.
func (s *Store) Restore(c Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := append([]Connection(nil), s.conns...)
	for i := range next {
		if next[i].ID == c.ID {
			next[i] = c
			return s.save(next)
		}
	}
	return ErrNotFound
}

// Delete removes a connection.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.conns {
		if s.conns[i].ID == id {
			next := slices.Delete(slices.Clone(s.conns), i, i+1)
			return s.save(next)
		}
	}
	return ErrNotFound
}

// save publishes the candidate in memory only after replacing the file.
// The caller holds the lock.
func (s *Store) save(next []Connection) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(file{Version: fileVersion, Connections: next}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".connections-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	s.conns = next
	return nil
}

func newID() string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
