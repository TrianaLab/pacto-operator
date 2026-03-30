/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package loader

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
)

func TestTagCacheKey(t *testing.T) {
	tests := []struct {
		ref    string
		expect string
	}{
		{"ghcr.io/org/svc", "tags:ghcr.io/org/svc"},
		{"ghcr.io/org/svc:1.0.0", "tags:ghcr.io/org/svc"},
		{"ghcr.io/org/svc@sha256:abc123", "tags:ghcr.io/org/svc"},
		{"ghcr.io/org/svc:1.0.0@sha256:abc123", "tags:ghcr.io/org/svc"},
		{"registry:5000/org/svc", "tags:registry:5000/org/svc"},
		{"registry:5000/org/svc:2.0.0", "tags:registry:5000/org/svc"},
	}

	for _, tt := range tests {
		got := tagCacheKey(tt.ref)
		if got != tt.expect {
			t.Errorf("tagCacheKey(%q) = %q, want %q", tt.ref, got, tt.expect)
		}
	}
}

func TestTagCacheKey_SameRepoSharesKey(t *testing.T) {
	// All forms of the same repo must produce the same cache key
	unversioned := tagCacheKey("ghcr.io/org/svc")
	tagged := tagCacheKey("ghcr.io/org/svc:1.0.0")
	digest := tagCacheKey("ghcr.io/org/svc@sha256:abcdef")

	if unversioned != tagged {
		t.Errorf("unversioned %q != tagged %q", unversioned, tagged)
	}
	if unversioned != digest {
		t.Errorf("unversioned %q != digest %q", unversioned, digest)
	}
}

func TestListTags_CachesResults(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	// Pre-populate the cache to simulate a previous call
	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0", "2.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// Should return cached result without hitting the (nil) OCI puller
	tags, err := l.ListTags(t.Context(), "ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 2 || tags[0] != "1.0.0" {
		t.Fatalf("expected cached tags [1.0.0 2.0.0], got %v", tags)
	}
}

func TestListTags_CacheExpiry(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	// Pre-populate with an expired entry
	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0"},
		expiresAt: time.Now().Add(-1 * time.Second), // expired
	}

	// Should NOT return expired cache — will fail calling the real OCI puller
	// (which would error since there's no real registry), proving the cache was skipped
	_, err := l.ListTags(t.Context(), "ghcr.io/org/svc", nil)
	if err == nil {
		t.Fatal("expected error from real OCI call after cache expiry")
	}
}

func TestListTags_EmptyRef(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
	}

	tags, err := l.ListTags(t.Context(), "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tags != nil {
		t.Fatalf("expected nil tags for empty ref, got %v", tags)
	}
}

func TestListTags_TaggedRefSharesCacheWithUnversioned(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	// Cache under unversioned ref
	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0", "2.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// Query with tagged ref — should hit the same cache entry
	tags, err := l.ListTags(t.Context(), "ghcr.io/org/svc:1.0.0", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("expected cached tags from unversioned entry, got %v", tags)
	}
}

func TestListTags_OCIPrefixNormalized(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"3.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// oci:// prefix should be stripped before cache lookup
	tags, err := l.ListTags(t.Context(), "oci://ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 1 || tags[0] != "3.0.0" {
		t.Fatalf("expected cached tags [3.0.0], got %v", tags)
	}
}

// ---------------------------------------------------------------------------
// BUG-2: authOverride bypasses cache
// ---------------------------------------------------------------------------

func TestListTags_AuthOverrideBypassesCache(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	// Pre-populate cache
	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// With authOverride, cache should be bypassed — the real OCI puller
	// will fail (no registry), proving the cache was not used
	auth := &authn.AuthConfig{Username: "user", Password: "pass"}
	_, err := l.ListTags(t.Context(), "ghcr.io/org/svc", auth)
	if err == nil {
		t.Fatal("expected error from real OCI call — cache should be bypassed when authOverride is set")
	}
}

func TestListTags_NilAuthUsesCache(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// Without authOverride, cache should be used
	tags, err := l.ListTags(t.Context(), "ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("expected cached tags, got %v", tags)
	}
}

func TestLoad_AuthOverrideBypassesCache(t *testing.T) {
	l := &Loader{
		cache:    make(map[string]cacheEntry),
		cacheTTL: 30 * time.Second,
		oci:      &OCIPuller{},
	}

	// Pre-populate cache with an error
	key := "oci:ghcr.io/org/svc"
	l.cache[key] = cacheEntry{
		result:    nil,
		err:       fmt.Errorf("cached auth error"),
		expiresAt: time.Now().Add(30 * time.Second),
	}

	// Without auth, should return cached error
	_, err := l.Load(t.Context(), "ghcr.io/org/svc", "", nil)
	if err == nil || err.Error() != "cached auth error" {
		t.Fatalf("expected cached error, got: %v", err)
	}

	// With authOverride, should bypass cache and hit real OCI (which will fail differently)
	auth := &authn.AuthConfig{Username: "user", Password: "pass"}
	_, err = l.Load(t.Context(), "ghcr.io/org/svc", "", auth)
	if err == nil {
		t.Fatal("expected error from real OCI call")
	}
	if err.Error() == "cached auth error" {
		t.Fatal("got cached error — authOverride should bypass cache")
	}
}
