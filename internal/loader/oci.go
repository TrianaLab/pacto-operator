/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package loader

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing/fstest"

	"github.com/Masterminds/semver/v3"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/trianalab/pacto/pkg/contract"
)

const (
	maxFileSize  = 10 << 20 // 10 MB per file
	maxTotalSize = 50 << 20 // 50 MB total
)

// OCIPuller pulls Pacto contract bundles from OCI registries.
type OCIPuller struct {
	keychain authn.Keychain
}

// NewOCIPuller creates an OCIPuller with a multi-level keychain:
// 1. PACTO_REGISTRY_TOKEN or PACTO_REGISTRY_USERNAME/PASSWORD env vars
// 2. gh CLI token (for ghcr.io)
// 3. Default Docker keychain
func NewOCIPuller() *OCIPuller {
	keychains := make([]authn.Keychain, 0, 3)

	if token := os.Getenv("PACTO_REGISTRY_TOKEN"); token != "" {
		keychains = append(keychains, staticKeychain{auth: &authn.AuthConfig{RegistryToken: token}})
	} else if user, pass := os.Getenv("PACTO_REGISTRY_USERNAME"), os.Getenv("PACTO_REGISTRY_PASSWORD"); user != "" && pass != "" {
		keychains = append(keychains, staticKeychain{auth: &authn.AuthConfig{Username: user, Password: pass}})
	}

	keychains = append(keychains, &ghKeychain{})
	keychains = append(keychains, authn.DefaultKeychain)

	return &OCIPuller{
		keychain: authn.NewMultiKeychain(keychains...),
	}
}

// Pull fetches a Pacto bundle from an OCI registry reference.
// The ref may have an "oci://" prefix which is stripped.
// If the ref has no explicit tag or digest, the highest semver tag is resolved.
func (p *OCIPuller) Pull(ctx context.Context, ref string) (*LoadResult, error) {
	ref = strings.TrimPrefix(ref, "oci://")

	// Resolve tag if not explicitly specified
	resolvedRef, err := p.resolveTag(ctx, ref)
	if err != nil {
		slog.Warn("Tag resolution failed, using original ref", "ref", ref, "error", err)
		resolvedRef = ref
	} else if resolvedRef != ref {
		slog.Info("Resolved OCI tag", "original", ref, "resolved", resolvedRef)
	}
	ref = resolvedRef

	parsedRef, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid OCI reference %q: %w", ref, err)
	}

	img, err := remote.Image(parsedRef,
		remote.WithAuthFromKeychain(p.keychain),
		remote.WithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %q: %w", ref, err)
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("failed to get layers from %q: %w", ref, err)
	}
	if len(layers) == 0 {
		return nil, fmt.Errorf("image %q has no layers", ref)
	}

	// Extract the first layer (Pacto bundles use a single tar layer).
	rc, err := layers[0].Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("failed to read layer from %q: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()

	bundleFS, err := extractTar(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to extract bundle from %q: %w", ref, err)
	}

	rawYAML, err := fs.ReadFile(bundleFS, "pacto.yaml")
	if err != nil {
		return nil, fmt.Errorf("bundle %q missing pacto.yaml: %w", ref, err)
	}

	c, err := contract.Parse(bytes.NewReader(rawYAML))
	if err != nil {
		return nil, fmt.Errorf("failed to parse contract from %q: %w", ref, err)
	}

	return &LoadResult{Contract: c, RawYAML: rawYAML, BundleFS: bundleFS, ResolvedRef: ref}, nil
}

// extractTar reads a tar stream into an in-memory filesystem.
func extractTar(r io.Reader) (fs.FS, error) {
	memFS := fstest.MapFS{}
	tr := tar.NewReader(r)
	var totalSize int64

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar entry: %w", err)
		}

		entryName := filepath.ToSlash(strings.TrimPrefix(header.Name, "./"))
		if entryName == "" || entryName == "." {
			continue
		}
		if strings.Contains(entryName, "..") {
			return nil, fmt.Errorf("invalid path in tar: %s", header.Name)
		}

		if header.Typeflag == tar.TypeDir {
			memFS[entryName] = &fstest.MapFile{Mode: fs.ModeDir | 0755}
			continue
		}

		data, err := io.ReadAll(io.LimitReader(tr, maxFileSize+1))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", entryName, err)
		}
		if int64(len(data)) > maxFileSize {
			return nil, fmt.Errorf("file %s exceeds maximum size of %d bytes", entryName, maxFileSize)
		}

		totalSize += int64(len(data))
		if totalSize > maxTotalSize {
			return nil, fmt.Errorf("extracted bundle exceeds maximum total size of %d bytes", maxTotalSize)
		}

		memFS[entryName] = &fstest.MapFile{Data: data, Mode: 0644}
	}

	return memFS, nil
}

// --- Auth keychains (same pattern as Pacto CLI) ---

type staticKeychain struct {
	auth *authn.AuthConfig
}

func (k staticKeychain) Resolve(_ authn.Resource) (authn.Authenticator, error) {
	return authn.FromConfig(*k.auth), nil
}

// ghKeychain uses the gh CLI to get tokens for GitHub registries.
type ghKeychain struct{}

func (k *ghKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	registry := target.RegistryStr()
	if registry != "ghcr.io" && registry != "docker.pkg.github.com" {
		return authn.Anonymous, nil
	}

	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return authn.Anonymous, nil
	}

	token := strings.TrimSpace(string(out))
	if token == "" {
		return authn.Anonymous, nil
	}

	return authn.FromConfig(authn.AuthConfig{
		Username: "x-access-token",
		Password: token,
	}), nil
}

// ListTags returns all semver tags for an OCI repository, sorted ascending.
func (p *OCIPuller) ListTags(ctx context.Context, ref string) ([]string, error) {
	ref = strings.TrimPrefix(ref, "oci://")
	// Strip any existing tag
	if idx := strings.LastIndex(ref, ":"); idx > strings.LastIndex(ref, "/") {
		ref = ref[:idx]
	}

	repo, err := name.NewRepository(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid repository %q: %w", ref, err)
	}

	tags, err := remote.List(repo,
		remote.WithAuthFromKeychain(p.keychain),
		remote.WithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags for %q: %w", ref, err)
	}

	var semverTags []string
	for _, t := range tags {
		if _, err := semver.NewVersion(t); err == nil {
			semverTags = append(semverTags, t)
		}
	}

	sort.Slice(semverTags, func(i, j int) bool {
		vi, _ := semver.NewVersion(semverTags[i])
		vj, _ := semver.NewVersion(semverTags[j])
		return vi.LessThan(vj)
	})

	return semverTags, nil
}

// --- OCI tag resolution ---
// Mirrors the logic from github.com/trianalab/pacto/internal/oci/resolve.go
// which is not importable (internal package).

// hasExplicitTag reports whether an OCI reference includes an explicit tag or digest.
func hasExplicitTag(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	return lastColon > lastSlash
}

// resolveTag resolves a tag-less OCI reference by listing available tags
// and selecting the highest semver version.
func (p *OCIPuller) resolveTag(ctx context.Context, ref string) (string, error) {
	if hasExplicitTag(ref) {
		return ref, nil
	}

	repo, err := name.NewRepository(ref)
	if err != nil {
		return "", fmt.Errorf("invalid repository %q: %w", ref, err)
	}

	tags, err := remote.List(repo,
		remote.WithAuthFromKeychain(p.keychain),
		remote.WithContext(ctx),
	)
	if err != nil {
		return "", fmt.Errorf("failed to list tags for %q: %w", ref, err)
	}

	best, err := bestTag(tags)
	if err != nil {
		return "", err
	}

	return ref + ":" + best, nil
}

// bestTag selects the highest semver tag from a list.
func bestTag(tags []string) (string, error) {
	var versions []*semver.Version
	for _, t := range tags {
		v, err := semver.NewVersion(t)
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no semver tags found")
	}
	sort.Sort(semver.Collection(versions))
	return versions[len(versions)-1].Original(), nil
}
