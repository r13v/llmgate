package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

type Cache struct {
	mu        sync.Mutex
	modelList map[string]modelListCacheEntry
	probe     map[string]probeCacheEntry
}

type modelListCacheEntry struct {
	result ModelListResult
	err    *Error
}

type probeCacheEntry struct {
	result ProbeResult
	err    *Error
}

func NewCache() *Cache {
	return &Cache{
		modelList: make(map[string]modelListCacheEntry),
		probe:     make(map[string]probeCacheEntry),
	}
}

func (c *Cache) String() string {
	if c == nil {
		return "gateway cache: disabled"
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("gateway cache: %d model list entries, %d probe entries", len(c.modelList), len(c.probe))
}

func (c *Cache) getModelList(key string, bypassFailed bool) (ModelListResult, *Error, bool) {
	if c == nil {
		return ModelListResult{}, nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.modelList[key]
	if !ok {
		return ModelListResult{}, nil, false
	}
	if entry.err != nil {
		if bypassFailed {
			return ModelListResult{}, nil, false
		}
		return ModelListResult{}, entry.err.cachedCopy(), true
	}
	result := entry.result
	result.Cached = true
	result.Summary = cachedSummary(result.Summary)
	return result, nil, true
}

func (c *Cache) setModelList(key string, result ModelListResult, err *Error) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	result.Cached = false
	c.modelList[key] = modelListCacheEntry{result: result, err: err.cacheCopy()}
}

func (c *Cache) getProbe(key string, bypassFailed bool) (ProbeResult, *Error, bool) {
	if c == nil {
		return ProbeResult{}, nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.probe[key]
	if !ok {
		return ProbeResult{}, nil, false
	}
	if entry.err != nil {
		if bypassFailed {
			return ProbeResult{}, nil, false
		}
		return ProbeResult{}, entry.err.cachedCopy(), true
	}
	result := entry.result
	result.Cached = true
	result.Summary = cachedSummary(result.Summary)
	return result, nil, true
}

func (c *Cache) setProbe(key string, result ProbeResult, err *Error) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	result.Cached = false
	c.probe[key] = probeCacheEntry{result: result, err: err.cacheCopy()}
}

func cacheKey(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func cachedSummary(summary string) string {
	if summary == "" {
		return "(cached)"
	}
	if containsCached(summary) {
		return summary
	}
	return summary + " (cached)"
}

func containsCached(summary string) bool {
	return len(summary) >= len("(cached)") && summary[len(summary)-len("(cached)"):] == "(cached)"
}
