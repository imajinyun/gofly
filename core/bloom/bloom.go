// Package bloom provides in-memory and distributed bloom filter implementations
// for negative caching and set-membership tests.
package bloom

import (
	"hash/fnv"
	"math"
	"sync"
)

// optimalParams computes the bit count (m) and hash count (k) for the desired
// capacity n and false-positive probability p using the standard formulas.
func optimalParams(n uint64, p float64) (bits uint64, hashes int) {
	if n == 0 {
		n = 1
	}
	if p <= 0 || p >= 1 {
		p = 0.01
	}
	m := math.Ceil(-(float64(n) * math.Log(p)) / (math.Ln2 * math.Ln2))
	if m < 1 {
		m = 1
	}
	k := math.Round((m / float64(n)) * math.Ln2)
	if k < 1 {
		k = 1
	}
	return uint64(m), int(k)
}

// offsets returns the k bit positions for data using double hashing derived
// from a single 64-bit FNV hash (Kirsch-Mitzenmacher technique).
func offsets(data []byte, bits uint64, hashes int) []uint64 {
	h := fnv.New64a()
	_, _ = h.Write(data)
	sum := h.Sum64()
	h1 := sum & 0xffffffff
	h2 := sum >> 32
	if h2 == 0 {
		h2 = 0x9e3779b97f4a7c15
	}
	result := make([]uint64, hashes)
	for i := 0; i < hashes; i++ {
		combined := h1 + uint64(i)*h2
		result[i] = combined % bits
	}
	return result
}

// Filter is an in-process bloom filter backed by a packed bit array.
type Filter struct {
	mu     sync.RWMutex
	words  []uint64
	bits   uint64
	hashes int
	added  uint64
}

// New creates an in-memory bloom filter sized for n elements at false-positive
// probability p.
func New(n uint64, p float64) *Filter {
	bits, hashes := optimalParams(n, p)
	return &Filter{
		words:  make([]uint64, (bits+63)/64),
		bits:   bits,
		hashes: hashes,
	}
}

// Add inserts data into the filter.
func (f *Filter) Add(data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, off := range offsets(data, f.bits, f.hashes) {
		f.words[off/64] |= 1 << (off % 64)
	}
	f.added++
}

// AddString inserts a string key.
func (f *Filter) AddString(key string) { f.Add([]byte(key)) }

// Contains reports whether data may be present. False positives are possible;
// false negatives are not.
func (f *Filter) Contains(data []byte) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, off := range offsets(data, f.bits, f.hashes) {
		if f.words[off/64]&(1<<(off%64)) == 0 {
			return false
		}
	}
	return true
}

// ContainsString reports possible membership of a string key.
func (f *Filter) ContainsString(key string) bool { return f.Contains([]byte(key)) }

// Reset clears the filter.
func (f *Filter) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.words {
		f.words[i] = 0
	}
	f.added = 0
}

// Stats describes the filter sizing and load.
type Stats struct {
	Bits   uint64 `json:"bits"`
	Hashes int    `json:"hashes"`
	Added  uint64 `json:"added"`
}

// Stats returns sizing information.
func (f *Filter) Stats() Stats {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return Stats{Bits: f.bits, Hashes: f.hashes, Added: f.added}
}
