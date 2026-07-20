# Cache Speed and Capacity Evaluation

Date: 2026-07-20

## Test Method

`scripts/cache-benchmark.sh` placed one deterministic 64 MiB OCI object in the local external-registry fixture, pulled it through Regstair, repeated hot manifest and blob requests, then referenced the same manifest and blob from a second repository. The upstream and Regstair were on the same Docker host, so the result understates the latency benefit expected with a remote registry. Blob throughput reflects the Linux page cache and is not a durable-disk benchmark.

## Results

| Measurement | Result |
| --- | --- |
| Cold manifest request including 64 MiB cache population | 71.873 ms |
| Hot manifest requests | 1.345-1.630 ms |
| Approximate cold-to-hot latency improvement | 44-53x; about 50x at the median |
| Hot cached 64 MiB blob | 12.639-17.472 ms; 3.84-5.31 GB/s |
| Direct local-upstream 64 MiB blob | 32.670-36.066 ms; 1.86-2.05 GB/s |
| Physical objects after two repositories reused the content | 2: one 253-byte manifest and one 67,108,864-byte blob |
| Total Regstair volume after test | 67,462,581 bytes |
| Metadata portion | 341,176 bytes |
| Host filesystem free space during test | 159 GiB of 929 GiB; filesystem already 83% used |

## Effectiveness

### Speed

The hot path is effective. A fresh tag mapping avoids every upstream request for 24 hours, opens the manifest directly by digest, and serves blobs as ordinary files. The local test produced about a 50x manifest-latency reduction and roughly twice the throughput of the local Registry fixture.

The first pull is not optimized for latency. Regstair downloads, hashes, and persists the manifest and every referenced blob before returning the manifest. The client then requests and reads the same blobs from Regstair. This provides atomic complete-image caching and offline replay, but prevents streaming and doubles first-pull data movement through the host. Large uncached images will have a noticeably slower start than a direct or streaming proxy.

Blob serving also calls `ListBlobs` to determine `Content-Length`. That scans, stats, and sorts the entire flat blob directory for every blob request. The measured two-object cache hides this cost; latency will grow with object count even though opening a known digest is otherwise constant-time.

### Capacity

Digest deduplication is effective and global. Reusing the same 64 MiB content and identical manifest from two repositories created no additional blob or manifest file. Physical payload utilization in this small test was approximately 99.5%; most non-payload space was the existing SQLite database.

Capacity control is not production-ready:

- no configured byte or object quota;
- no low/high-water marks or reserved-space check;
- no LRU, LFU, TTL-based blob eviction, or garbage collection;
- no reference counting for safe deletion of shared content;
- no per-source, route, tenant, or repository quota;
- no startup/readiness failure based on minimum free space;
- append-only request, provenance, and audit history has no retention policy;
- concurrent first requests for the same missing digest can each write a full temporary copy before one final file wins.

The practical capacity is therefore all free space on the backing filesystem. On the measured host that was 159 GiB, but consuming it would also endanger Docker and other workloads. Regstair currently has no mechanism to stop before filesystem exhaustion.

Tag mappings are fresh for 24 hours. Digest references remain cacheable indefinitely. Once a tag mapping expires, Regstair goes upstream and does not serve that expired mapping as stale on failure, so outage resilience for tag pulls is bounded by the freshness window.

## Recommended Next Work

1. Replace blob-size lookup through `ListBlobs` with direct `stat` by digest, and add `Accept-Ranges`/HTTP range support.
2. Add cache metrics: physical bytes, object count, logical referenced bytes, deduplication ratio, hit ratio, eviction count, free bytes, and cold-fill duration.
3. Add configurable maximum bytes plus low/high-water marks and readiness/alert thresholds.
4. Track references from tag mappings/manifests to blobs, then implement safe mark-and-sweep or reference-aware LRU eviction.
5. Add request/provenance/audit retention or archival independently from blob eviction.
6. Coalesce concurrent fills for the same digest and repository reference.
7. Evaluate streaming or tee-to-cache for cold blobs so the first client does not wait for complete cache population before transfer.
8. Define stale-if-error behavior separately from normal 24-hour tag freshness.

Until items 2-4 exist, the cache is suitable for controlled environments with externally monitored and generously sized storage, but not for unattended capacity management on a shared Docker filesystem.
