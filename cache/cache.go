package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	cacheMutex sync.RWMutex
	cacheIndex = make(map[string]string) // key → digest
	cacheLoaded = false
)

// getCachePath returns ~/.docksmith/cache/cache.json
func getCachePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docksmith", "cache", "cache.json")
}

// loadCache reads the cache index from disk (if exists)
func loadCache() error {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	
	if cacheLoaded {
		return nil
	}
	
	path := getCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cacheLoaded = true
			return nil // no cache file yet is ok
		}
		return err
	}
	
	if err := json.Unmarshal(data, &cacheIndex); err != nil {
		return err
	}
	
	cacheLoaded = true
	return nil
}

// Lookup checks the cache index for a given key.
// Returns the stored layer digest and true if found, or "", false otherwise.
func Lookup(key string) (string, bool) {
	if err := loadCache(); err != nil {
		return "", false
	}
	
	cacheMutex.RLock()
	defer cacheMutex.RUnlock()
	
	digest, ok := cacheIndex[key]
	return digest, ok
}

// Store writes a key → layerDigest mapping into the cache index.
func Store(key, layerDigest string) {
	if err := loadCache(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cache load failed: %v\n", err)
	}
	
	cacheMutex.Lock()
	cacheIndex[key] = layerDigest
	cacheMutex.Unlock()
	
	// persist to disk
	path := getCachePath()
	data, _ := json.MarshalIndent(cacheIndex, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cache persist failed: %v\n", err)
	}
}
