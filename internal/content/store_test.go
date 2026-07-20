package content

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreWritesVerifiedBlobByDigest(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	digest := "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69"
	desc, err := store.PutBlob(context.Background(), digest, bytes.NewReader([]byte("hello regstair")))
	if err != nil {
		t.Fatalf("PutBlob() error = %v", err)
	}

	if got, want := desc.Digest, digest; got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
	if got, want := desc.Size, int64(14); got != want {
		t.Fatalf("size = %d, want %d", got, want)
	}

	rc, err := store.OpenBlob(context.Background(), digest)
	if err != nil {
		t.Fatalf("OpenBlob() error = %v", err)
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got, want := string(body), "hello regstair"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestFileStoreRejectsDigestMismatch(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	err = putOnlyError(store.PutBlob(context.Background(), "sha256:0000000000000000000000000000000000000000000000000000000000000000", bytes.NewReader([]byte("hello regstair"))))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("PutBlob() error = %v, want ErrDigestMismatch", err)
	}
}

func TestFileStoreDoesNotDuplicateExistingBlob(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	digest := "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69"
	if _, err := store.PutBlob(context.Background(), digest, bytes.NewReader([]byte("hello regstair"))); err != nil {
		t.Fatalf("first PutBlob() error = %v", err)
	}
	if _, err := store.PutBlob(context.Background(), digest, bytes.NewReader([]byte("hello regstair"))); err != nil {
		t.Fatalf("second PutBlob() error = %v", err)
	}

	var blobFiles int
	if err := filepath.WalkDir(filepath.Join(root, "blobs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			blobFiles++
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
	if got, want := blobFiles, 1; got != want {
		t.Fatalf("blob file count = %d, want %d", got, want)
	}
}

func TestFileStoreReportsMissingBlob(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	_, err = store.OpenBlob(context.Background(), "sha256:315f5bdb76d078c43b8ac0064e4a0164612b1fce77c869345bfc94c75894edd3")
	if !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("OpenBlob() error = %v, want ErrBlobNotFound", err)
	}
}

func TestFileStoreListsUniqueBlobs(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	if _, err := store.PutBlob(context.Background(), "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69", bytes.NewReader([]byte("hello regstair"))); err != nil {
		t.Fatalf("PutBlob(first) error = %v", err)
	}
	if _, err := store.PutBlob(context.Background(), "sha256:7a19147749ce097f32b13072bccba494b5e717d4f23fbde50f7514059e109ad1", bytes.NewReader([]byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{}}`))); err != nil {
		t.Fatalf("PutBlob(second) error = %v", err)
	}
	if _, err := store.PutBlob(context.Background(), "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69", bytes.NewReader([]byte("hello regstair"))); err != nil {
		t.Fatalf("PutBlob(duplicate) error = %v", err)
	}

	blobs, err := store.ListBlobs(context.Background())
	if err != nil {
		t.Fatalf("ListBlobs() error = %v", err)
	}
	if len(blobs) != 2 {
		t.Fatalf("blob count = %d, want 2", len(blobs))
	}
	if got, want := blobs[0].Digest, "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69"; got != want {
		t.Fatalf("first digest = %q, want %q", got, want)
	}
}

func TestFileStoreRejectsUnsupportedDigestAlgorithm(t *testing.T) {
	_, err := NewDescriptor("md5:5d41402abc4b2a76b9719d911017c592", 5)
	if err == nil {
		t.Fatal("NewDescriptor() error = nil, want unsupported digest algorithm error")
	}
}

func putOnlyError(_ Descriptor, err error) error {
	return err
}
