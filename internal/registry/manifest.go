package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func ParseManifest(body []byte) (Manifest, error) {
	var raw struct {
		MediaType string `json:"mediaType"`
		Config    struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Manifest{}, err
	}

	digests := make([]string, 0, len(raw.Layers)+1)
	if raw.Config.Digest != "" {
		digests = append(digests, raw.Config.Digest)
	}
	for _, layer := range raw.Layers {
		if layer.Digest != "" {
			digests = append(digests, layer.Digest)
		}
	}

	mediaType := raw.MediaType
	if mediaType == "" {
		mediaType = "application/vnd.oci.image.manifest.v1+json"
	}

	return Manifest{
		Descriptor: Descriptor{
			MediaType: mediaType,
			Digest:    digestBytes(body),
			Size:      int64(len(body)),
		},
		Content:     append([]byte(nil), body...),
		BlobDigests: digests,
	}, nil
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
