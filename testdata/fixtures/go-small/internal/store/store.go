// Package store provides an in-memory key-value store.
package store

import (
	"errors"
	"sync"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("not found")

// Store is a thread-safe key-value store.
type Store struct {
	mu    sync.RWMutex
	items map[string]string
}

// Reader reads values by key.
type Reader interface {
	Get(key string) (string, error)
}

// New creates an empty Store.
func New() *Store {
	return &Store{items: make(map[string]string)}
}

// Get returns the value for key, or ErrNotFound.
func (s *Store) Get(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.items[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set stores value under key.
func (s *Store) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = normalize(value)
}

// Len reports the number of stored items.
func (s *Store) Len() int {
	return len(s.items)
}

// Item is a key-value pair snapshot.
type Item struct {
	Key   string
	Value string
}

// Describe renders the item as "key=value".
func (i Item) Describe() string {
	return i.Key + "=" + i.Value
}

func normalize(v string) string {
	if v == "" {
		return "<empty>"
	}
	return v
}
