package cache

import (
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

type EntityCache[T any] struct {
	store *expirable.LRU[string, T]
}

func NewEntityCache[T any](size int, ttl time.Duration) *EntityCache[T] {
	if size < 1 {
		size = 1
	}
	if ttl <= 0 {
		ttl = time.Second
	}
	return &EntityCache[T]{
		store: expirable.NewLRU[string, T](size, nil, ttl),
	}
}

func (c *EntityCache[T]) Add(key string, value T) bool {
	return c.store.Add(key, value)
}

func (c *EntityCache[T]) Get(key string) (T, bool) {
	return c.store.Get(key)
}

func (c *EntityCache[T]) Remove(key string) bool {
	return c.store.Remove(key)
}
