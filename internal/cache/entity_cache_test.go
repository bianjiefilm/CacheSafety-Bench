package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEntityCacheStoresAndRemoves(t *testing.T) {
	c := NewEntityCache[int](8, time.Minute)
	c.Add("one", 1)

	got, ok := c.Get("one")
	require.True(t, ok)
	require.Equal(t, 1, got)

	require.True(t, c.Remove("one"))
	_, ok = c.Get("one")
	require.False(t, ok)
}
