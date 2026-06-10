package store

// Cache wraps a Store with hit counting.
type Cache struct {
	backend *Store
	hits    int
}

// NewCache creates a Cache over a fresh Store.
func NewCache() *Cache {
	return &Cache{backend: New()}
}

// Lookup reads through to the backend and counts hits.
func (c *Cache) Lookup(key string) (string, error) {
	v, err := c.backend.Get(key)
	if err == nil {
		c.hits++
	}
	return v, err
}

// Warm preloads the cache with pairs.
func (c *Cache) Warm(pairs map[string]string) {
	for k, v := range pairs {
		c.backend.Set(k, v)
	}
}
