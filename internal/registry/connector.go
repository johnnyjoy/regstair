package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
)

var (
	ErrDigestMismatch        = errors.New("digest mismatch")
	ErrNotFound              = errors.New("registry object not found")
	ErrAuthentication        = errors.New("registry authentication failed")
	ErrAuthorization         = errors.New("registry authorization failed")
	ErrUnavailable           = errors.New("registry unavailable")
	ErrCredentialRequired    = errors.New("registry credential required")
	ErrCredentialUnavailable = errors.New("registry credential unavailable")
)

type Descriptor struct {
	MediaType string
	Digest    string
	Size      int64
}

type Manifest struct {
	Descriptor
	Content     []byte
	BlobDigests []string
}

type Connector interface {
	Name() string
	Health(ctx context.Context) error
	ResolveManifest(ctx context.Context, repository string, reference string) (Manifest, error)
	OpenBlob(ctx context.Context, repository string, digest string) (io.ReadCloser, Descriptor, error)
	PutBlob(ctx context.Context, repository string, digest string, body io.Reader) (Descriptor, error)
	PutManifest(ctx context.Context, repository string, reference string, manifest Manifest) (Descriptor, error)
}

type FakeConnector struct {
	mu         sync.RWMutex
	name       string
	available  bool
	manifests  map[string]Manifest
	blobs      map[string][]byte
	repository map[string]map[string]struct{}
}

func NewFakeConnector(name string) *FakeConnector {
	return &FakeConnector{
		name:       name,
		available:  true,
		manifests:  map[string]Manifest{},
		blobs:      map[string][]byte{},
		repository: map[string]map[string]struct{}{},
	}
}

func (c *FakeConnector) Name() string {
	return c.name
}

func (c *FakeConnector) SetAvailable(available bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.available = available
}

func (c *FakeConnector) AddBlob(digest string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blobs[digest] = append([]byte(nil), body...)
}

func (c *FakeConnector) AddManifest(repository string, reference string, manifest Manifest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addManifestLocked(repository, reference, manifest)
}

func (c *FakeConnector) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.available {
		return ErrUnavailable
	}
	return nil
}

func (c *FakeConnector) ResolveManifest(ctx context.Context, repository string, reference string) (Manifest, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return Manifest{}, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	manifest, ok := c.manifests[manifestKey(repository, reference)]
	if !ok {
		return Manifest{}, fmt.Errorf("%w: manifest %s:%s", ErrNotFound, repository, reference)
	}
	return cloneManifest(manifest), nil
}

func (c *FakeConnector) OpenBlob(ctx context.Context, repository string, digest string) (io.ReadCloser, Descriptor, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return nil, Descriptor{}, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	body, ok := c.blobs[digest]
	if !ok {
		return nil, Descriptor{}, fmt.Errorf("%w: blob %s", ErrNotFound, digest)
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), body...))), Descriptor{Digest: digest, Size: int64(len(body))}, nil
}

func (c *FakeConnector) PutBlob(ctx context.Context, repository string, digest string, body io.Reader) (Descriptor, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return Descriptor{}, err
	}

	content, err := io.ReadAll(body)
	if err != nil {
		return Descriptor{}, fmt.Errorf("read blob: %w", err)
	}
	if got := sha256Digest(content); got != digest {
		return Descriptor{}, fmt.Errorf("%w: got %s want %s", ErrDigestMismatch, got, digest)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.blobs[digest] = append([]byte(nil), content...)
	if _, ok := c.repository[repository]; !ok {
		c.repository[repository] = map[string]struct{}{}
	}
	c.repository[repository][digest] = struct{}{}

	return Descriptor{Digest: digest, Size: int64(len(content))}, nil
}

func (c *FakeConnector) PutManifest(ctx context.Context, repository string, reference string, manifest Manifest) (Descriptor, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return Descriptor{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, digest := range manifest.BlobDigests {
		if _, ok := c.blobs[digest]; !ok {
			return Descriptor{}, fmt.Errorf("%w: blob %s", ErrNotFound, digest)
		}
	}
	c.addManifestLocked(repository, reference, manifest)
	return manifest.Descriptor, nil
}

func (c *FakeConnector) checkAvailable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.available {
		return ErrUnavailable
	}
	return nil
}

func (c *FakeConnector) addManifestLocked(repository string, reference string, manifest Manifest) {
	manifest = cloneManifest(manifest)
	c.manifests[manifestKey(repository, reference)] = manifest
	if manifest.Digest != "" {
		c.manifests[manifestKey(repository, manifest.Digest)] = manifest
	}
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.Content = append([]byte(nil), manifest.Content...)
	manifest.BlobDigests = append([]string(nil), manifest.BlobDigests...)
	return manifest
}

func manifestKey(repository string, reference string) string {
	return repository + "@" + reference
}

func sha256Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
