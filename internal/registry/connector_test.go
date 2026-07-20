package registry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
)

const (
	testBlobDigest     = "sha256:01916477bcaa5cb015b1c92387adece9a93c70bb19b6db733aebfe66212bdf69"
	testManifestDigest = "sha256:bafebd36189ad3688b7b3915ea55d461e0bfcfbdde11e54b0a123999fb6be50f"
)

func TestFakeConnectorResolvesManifestByTagAndDigest(t *testing.T) {
	connector := NewFakeConnector("external-registry")
	manifest := Manifest{
		Descriptor: Descriptor{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    testManifestDigest,
			Size:      19,
		},
		Content:     []byte(`{"schemaVersion":2}`),
		BlobDigests: []string{testBlobDigest},
	}
	connector.AddManifest("library/nginx", "1.27", manifest)

	byTag, err := connector.ResolveManifest(context.Background(), "library/nginx", "1.27")
	if err != nil {
		t.Fatalf("ResolveManifest(tag) error = %v", err)
	}
	byDigest, err := connector.ResolveManifest(context.Background(), "library/nginx", testManifestDigest)
	if err != nil {
		t.Fatalf("ResolveManifest(digest) error = %v", err)
	}

	if byTag.Digest != testManifestDigest {
		t.Fatalf("tag digest = %q, want %q", byTag.Digest, testManifestDigest)
	}
	if !reflect.DeepEqual(byDigest, byTag) {
		t.Fatalf("digest lookup = %#v, want %#v", byDigest, byTag)
	}
}

func TestFakeConnectorReturnsNotFoundForMissingManifest(t *testing.T) {
	connector := NewFakeConnector("internal-registry")

	_, err := connector.ResolveManifest(context.Background(), "library/nginx", "1.27")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveManifest() error = %v, want ErrNotFound", err)
	}
}

func TestFakeConnectorReturnsUnavailableWhenDisabled(t *testing.T) {
	connector := NewFakeConnector("external-registry")
	connector.SetAvailable(false)

	_, err := connector.ResolveManifest(context.Background(), "library/nginx", "1.27")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ResolveManifest() error = %v, want ErrUnavailable", err)
	}

	if err := connector.Health(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Health() error = %v, want ErrUnavailable", err)
	}
}

func TestFakeConnectorReadsBlobByDigest(t *testing.T) {
	connector := NewFakeConnector("external-registry")
	connector.AddBlob(testBlobDigest, []byte("hello regstair"))

	rc, desc, err := connector.OpenBlob(context.Background(), "library/nginx", testBlobDigest)
	if err != nil {
		t.Fatalf("OpenBlob() error = %v", err)
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got, want := string(body), "hello regstair"; got != want {
		t.Fatalf("blob body = %q, want %q", got, want)
	}
	if got, want := desc.Size, int64(14); got != want {
		t.Fatalf("blob size = %d, want %d", got, want)
	}
}

func TestFakeConnectorPublishesBlobAndManifest(t *testing.T) {
	connector := NewFakeConnector("destination-registry")

	blob, err := connector.PutBlob(context.Background(), "team-a/service", testBlobDigest, bytes.NewReader([]byte("hello regstair")))
	if err != nil {
		t.Fatalf("PutBlob() error = %v", err)
	}
	if got, want := blob.Digest, testBlobDigest; got != want {
		t.Fatalf("blob digest = %q, want %q", got, want)
	}

	manifest := Manifest{
		Descriptor: Descriptor{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    testManifestDigest,
			Size:      19,
		},
		Content:     []byte(`{"schemaVersion":2}`),
		BlobDigests: []string{testBlobDigest},
	}
	desc, err := connector.PutManifest(context.Background(), "team-a/service", "4.1", manifest)
	if err != nil {
		t.Fatalf("PutManifest() error = %v", err)
	}
	if got, want := desc.Digest, testManifestDigest; got != want {
		t.Fatalf("manifest digest = %q, want %q", got, want)
	}

	got, err := connector.ResolveManifest(context.Background(), "team-a/service", "4.1")
	if err != nil {
		t.Fatalf("ResolveManifest() error = %v", err)
	}
	if got.Digest != testManifestDigest {
		t.Fatalf("resolved digest = %q, want %q", got.Digest, testManifestDigest)
	}
}

func TestFakeConnectorRejectsBlobDigestMismatch(t *testing.T) {
	connector := NewFakeConnector("destination-registry")

	_, err := connector.PutBlob(context.Background(), "team-a/service", "sha256:0000000000000000000000000000000000000000000000000000000000000000", bytes.NewReader([]byte("hello regstair")))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("PutBlob() error = %v, want ErrDigestMismatch", err)
	}
}

func TestFakeConnectorRejectsManifestReferencingMissingBlob(t *testing.T) {
	connector := NewFakeConnector("destination-registry")
	manifest := Manifest{
		Descriptor: Descriptor{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    testManifestDigest,
			Size:      19,
		},
		Content:     []byte(`{"schemaVersion":2}`),
		BlobDigests: []string{testBlobDigest},
	}

	_, err := connector.PutManifest(context.Background(), "team-a/service", "4.1", manifest)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("PutManifest() error = %v, want ErrNotFound", err)
	}
}
