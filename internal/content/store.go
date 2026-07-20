package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrBlobNotFound      = errors.New("blob not found")
	ErrDigestMismatch    = errors.New("digest mismatch")
	ErrUnsupportedDigest = errors.New("unsupported digest")
)

type Descriptor struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type Store interface {
	PutBlob(ctx context.Context, digest string, content io.Reader) (Descriptor, error)
	OpenBlob(ctx context.Context, digest string) (io.ReadCloser, error)
	HasBlob(ctx context.Context, digest string) (bool, error)
	ListBlobs(ctx context.Context) ([]Descriptor, error)
}

type FileStore struct {
	root string
}

func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, fmt.Errorf("content store root is required")
	}
	if err := os.MkdirAll(filepath.Join(root, "blobs", "sha256"), 0o755); err != nil {
		return nil, fmt.Errorf("create content store: %w", err)
	}
	return &FileStore{root: root}, nil
}

func NewDescriptor(digest string, size int64) (Descriptor, error) {
	if _, _, err := parseDigest(digest); err != nil {
		return Descriptor{}, err
	}
	if size < 0 {
		return Descriptor{}, fmt.Errorf("descriptor size must not be negative")
	}
	return Descriptor{Digest: digest, Size: size}, nil
}

func (s *FileStore) PutBlob(ctx context.Context, digest string, content io.Reader) (Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}
	algorithm, encoded, err := parseDigest(digest)
	if err != nil {
		return Descriptor{}, err
	}

	finalPath := s.blobPath(algorithm, encoded)
	if exists, err := fileExists(finalPath); err != nil {
		return Descriptor{}, err
	} else if exists {
		size, err := fileSize(finalPath)
		if err != nil {
			return Descriptor{}, err
		}
		return Descriptor{Digest: digest, Size: size}, nil
	}

	temp, err := os.CreateTemp(filepath.Dir(finalPath), ".upload-*")
	if err != nil {
		return Descriptor{}, fmt.Errorf("create blob temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	hash := newHash(algorithm)
	size, copyErr := copyWithContext(ctx, temp, io.TeeReader(content, hash))
	closeErr := temp.Close()
	if copyErr != nil {
		return Descriptor{}, copyErr
	}
	if closeErr != nil {
		return Descriptor{}, fmt.Errorf("close blob temp file: %w", closeErr)
	}

	if got := algorithm + ":" + hex.EncodeToString(hash.Sum(nil)); got != digest {
		return Descriptor{}, fmt.Errorf("%w: got %s want %s", ErrDigestMismatch, got, digest)
	}

	if err := os.Rename(tempPath, finalPath); err != nil {
		if exists, existsErr := fileExists(finalPath); existsErr == nil && exists {
			return Descriptor{Digest: digest, Size: size}, nil
		}
		return Descriptor{}, fmt.Errorf("finalize blob: %w", err)
	}

	return Descriptor{Digest: digest, Size: size}, nil
}

func (s *FileStore) OpenBlob(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	algorithm, encoded, err := parseDigest(digest)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(s.blobPath(algorithm, encoded))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrBlobNotFound, digest)
	}
	if err != nil {
		return nil, fmt.Errorf("open blob: %w", err)
	}
	return file, nil
}

func (s *FileStore) HasBlob(ctx context.Context, digest string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	algorithm, encoded, err := parseDigest(digest)
	if err != nil {
		return false, err
	}
	return fileExists(s.blobPath(algorithm, encoded))
}

func (s *FileStore) ListBlobs(ctx context.Context) ([]Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	root := filepath.Join(s.root, "blobs", "sha256")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list blobs: %w", err)
	}

	blobs := make([]Descriptor, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() {
			continue
		}
		digest := "sha256:" + entry.Name()
		if _, _, err := parseDigest(digest); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat blob %s: %w", digest, err)
		}
		blobs = append(blobs, Descriptor{Digest: digest, Size: info.Size()})
	}

	sort.SliceStable(blobs, func(i, j int) bool {
		return blobs[i].Digest < blobs[j].Digest
	})
	return blobs, nil
}

func (s *FileStore) blobPath(algorithm, encoded string) string {
	return filepath.Join(s.root, "blobs", algorithm, encoded)
}

func parseDigest(digest string) (string, string, error) {
	algorithm, encoded, ok := strings.Cut(digest, ":")
	if !ok || algorithm == "" || encoded == "" {
		return "", "", fmt.Errorf("%w: %q", ErrUnsupportedDigest, digest)
	}
	if algorithm != "sha256" {
		return "", "", fmt.Errorf("%w: %q", ErrUnsupportedDigest, digest)
	}
	if len(encoded) != sha256.Size*2 {
		return "", "", fmt.Errorf("%w: invalid sha256 length for %q", ErrUnsupportedDigest, digest)
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		return "", "", fmt.Errorf("%w: invalid sha256 encoding for %q", ErrUnsupportedDigest, digest)
	}
	return algorithm, encoded, nil
}

func newHash(algorithm string) hash.Hash {
	switch algorithm {
	case "sha256":
		return sha256.New()
	default:
		panic("unsupported digest algorithm escaped validation")
	}
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		nr, er := src.Read(buffer)
		if nr > 0 {
			nw, ew := dst.Write(buffer[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er == io.EOF {
			return written, nil
		}
		if er != nil {
			return written, er
		}
	}
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
