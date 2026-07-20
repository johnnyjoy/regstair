package security

import (
	"testing"
	"time"
)

func TestFailureLimiterBlocksEitherDimensionAndRecovers(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	limiter := NewFailureLimiter(3, time.Minute, 5*time.Minute, func() time.Time { return now })
	keys := []string{"address:192.0.2.1", "account:alice"}
	for range 3 {
		if allowed, _ := limiter.Allow(keys...); !allowed {
			t.Fatal("attempt blocked before threshold")
		}
		limiter.Failure(keys...)
	}
	if allowed, retry := limiter.Allow(keys...); allowed || retry != 5*time.Minute {
		t.Fatalf("blocked attempt = %v, %v", allowed, retry)
	}
	if allowed, _ := limiter.Allow("address:192.0.2.2", "account:alice"); allowed {
		t.Fatal("account bucket did not block a different address")
	}
	now = now.Add(5 * time.Minute)
	if allowed, retry := limiter.Allow(keys...); !allowed || retry != 0 {
		t.Fatalf("recovered attempt = %v, %v", allowed, retry)
	}
	limiter.Failure(keys...)
	limiter.Success(keys...)
	if allowed, _ := limiter.Allow(keys...); !allowed {
		t.Fatal("success did not reset buckets")
	}
}

func TestFailureLimiterAddressBucketStopsAccountRotation(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	limiter := NewFailureLimiter(3, time.Minute, 5*time.Minute, func() time.Time { return now })
	for _, account := range []string{"alice", "bob", "carol"} {
		limiter.Failure("address:192.0.2.1", "account:"+account)
	}
	if allowed, _ := limiter.Allow("address:192.0.2.1", "account:dave"); allowed {
		t.Fatal("address bucket allowed username rotation")
	}
	if allowed, _ := limiter.Allow("address:192.0.2.2", "account:dave"); !allowed {
		t.Fatal("unrelated address and account were blocked")
	}
}
