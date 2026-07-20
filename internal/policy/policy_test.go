package policy

import (
	"reflect"
	"testing"
)

func TestMatchRouteUsesLowestPrecedenceMatchingRoute(t *testing.T) {
	engine, err := NewEngine(Config{
		Routes: []Route{
			{Name: "catch-all", Match: "**", Precedence: 100},
			{Name: "platform", Match: "platform/**", Precedence: 10},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	route, err := engine.MatchRoute("platform/api")
	if err != nil {
		t.Fatalf("MatchRoute() error = %v", err)
	}
	if route.Name != "platform" {
		t.Fatalf("matched route = %q, want platform", route.Name)
	}
}

func TestNewEngineRejectsAmbiguousRoutesAtSamePrecedence(t *testing.T) {
	_, err := NewEngine(Config{
		Routes: []Route{
			{Name: "library", Match: "library/**", Precedence: 10},
			{Name: "library-nginx", Match: "library/nginx", Precedence: 10},
		},
	})
	if err == nil {
		t.Fatal("NewEngine() error = nil, want ambiguity error")
	}
}

func TestNewEngineRejectsDuplicateRouteNames(t *testing.T) {
	_, err := NewEngine(Config{
		Routes: []Route{
			{Name: "library", Match: "library/**", Precedence: 10},
			{Name: "library", Match: "vendor/**", Precedence: 20},
		},
	})
	if err == nil {
		t.Fatal("NewEngine() error = nil, want duplicate route name error")
	}
}

func TestNewEngineRejectsUnknownPullSource(t *testing.T) {
	_, err := NewEngine(Config{
		Sources: []Source{{ID: "internal-curated"}},
		Routes: []Route{
			{
				Name:        "library",
				Match:       "library/**",
				Precedence:  10,
				PullSources: []string{"internal-curated", "docker-hub"},
			},
		},
	})
	if err == nil {
		t.Fatal("NewEngine() error = nil, want unknown pull source error")
	}
}

func TestNewEngineRejectsUnknownAuthoritativeSource(t *testing.T) {
	_, err := NewEngine(Config{
		Sources: []Source{{ID: "harbor-central"}},
		Routes: []Route{
			{
				Name:          "platform",
				Match:         "platform/**",
				Precedence:    10,
				PullSources:   []string{"harbor-central"},
				Authoritative: "harbor-dr",
			},
		},
	})
	if err == nil {
		t.Fatal("NewEngine() error = nil, want unknown authoritative source error")
	}
}

func TestNewEngineRejectsAuthoritativeSourceOutsidePullSequence(t *testing.T) {
	_, err := NewEngine(Config{
		Sources: []Source{{ID: "harbor-central"}, {ID: "harbor-dr"}},
		Routes: []Route{
			{
				Name:          "platform",
				Match:         "platform/**",
				Precedence:    10,
				PullSources:   []string{"harbor-dr"},
				Authoritative: "harbor-central",
			},
		},
	})
	if err == nil {
		t.Fatal("NewEngine() error = nil, want authoritative source outside pull sequence error")
	}
}

func TestNewEngineRejectsUnknownPushDestination(t *testing.T) {
	_, err := NewEngine(Config{
		Sources: []Source{{ID: "harbor-central"}},
		Routes: []Route{
			{
				Name:            "team-a",
				Match:           "team-a/**",
				Precedence:      10,
				PushDestination: "harbor-team-a",
			},
		},
	})
	if err == nil {
		t.Fatal("NewEngine() error = nil, want unknown push destination error")
	}
}

func TestResolvePullStopsWhenAuthoritativeFallbackIsForbidden(t *testing.T) {
	engine, err := NewEngine(Config{
		Routes: []Route{
			{
				Name: "internal-platform", Match: "platform/**", Precedence: 10,
				PullSources:      []string{"harbor-central", "docker-hub"},
				Authoritative:    "harbor-central",
				ExternalFallback: false,
			},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	decision, err := engine.ResolvePull(PullRequest{Repository: "platform/api", Reference: "1.0.0"})
	if err != nil {
		t.Fatalf("ResolvePull() error = %v", err)
	}

	wantSources := []string{"harbor-central"}
	if !reflect.DeepEqual(decision.CandidateSources, wantSources) {
		t.Fatalf("candidate sources = %#v, want %#v", decision.CandidateSources, wantSources)
	}
	if decision.ExternalFallbackAllowed {
		t.Fatal("external fallback allowed = true, want false")
	}
}

func TestResolvePullContinuesWhenExternalFallbackIsAllowed(t *testing.T) {
	engine, err := NewEngine(Config{
		Routes: []Route{
			{
				Name: "curated-library", Match: "library/**", Precedence: 10,
				PullSources:      []string{"internal-curated", "docker-hub"},
				Authoritative:    "internal-curated",
				ExternalFallback: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	decision, err := engine.ResolvePull(PullRequest{Repository: "library/nginx", Reference: "1.27"})
	if err != nil {
		t.Fatalf("ResolvePull() error = %v", err)
	}

	wantSources := []string{"internal-curated", "docker-hub"}
	if !reflect.DeepEqual(decision.CandidateSources, wantSources) {
		t.Fatalf("candidate sources = %#v, want %#v", decision.CandidateSources, wantSources)
	}
}

func TestResolvePullAppliesNamespaceRewrite(t *testing.T) {
	engine, err := NewEngine(Config{
		Routes: []Route{
			{
				Name: "library", Match: "library/**", Precedence: 10,
				PullSources: []string{"docker-hub"},
				Rewrite: Rewrite{
					StripPrefix: "library/",
					AddPrefix:   "library/",
				},
				ExternalFallback: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	decision, err := engine.ResolvePull(PullRequest{Repository: "library/nginx", Reference: "1.27"})
	if err != nil {
		t.Fatalf("ResolvePull() error = %v", err)
	}

	if got, want := decision.PhysicalRepository, "library/nginx"; got != want {
		t.Fatalf("physical repository = %q, want %q", got, want)
	}
}

func TestResolvePullNormalizesRepository(t *testing.T) {
	engine, err := NewEngine(Config{
		Routes: []Route{
			{Name: "platform", Match: "platform/**", Precedence: 10, PullSources: []string{"harbor-central"}},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	decision, err := engine.ResolvePull(PullRequest{Repository: " /platform/api/ ", Reference: "1.0.0"})
	if err != nil {
		t.Fatalf("ResolvePull() error = %v", err)
	}

	if got, want := decision.LogicalRepository, "platform/api"; got != want {
		t.Fatalf("logical repository = %q, want %q", got, want)
	}
}

func TestResolvePullRejectsInvalidRepository(t *testing.T) {
	engine, err := NewEngine(Config{
		Routes: []Route{
			{Name: "platform", Match: "platform/**", Precedence: 10, PullSources: []string{"harbor-central"}},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	_, err = engine.ResolvePull(PullRequest{Repository: "Platform/API", Reference: "1.0.0"})
	if err == nil {
		t.Fatal("ResolvePull() error = nil, want invalid repository error")
	}
}

func TestResolvePushSelectsDestination(t *testing.T) {
	engine, err := NewEngine(Config{
		Routes: []Route{
			{
				Name:            "team-a",
				Match:           "team-a/**",
				Precedence:      10,
				PushDestination: "harbor-team-a",
				Rewrite: Rewrite{
					StripPrefix: "team-a/",
					AddPrefix:   "production-team-a/",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	decision, err := engine.ResolvePush(PushRequest{Repository: "team-a/service", Reference: "4.1"})
	if err != nil {
		t.Fatalf("ResolvePush() error = %v", err)
	}

	if got, want := decision.Destination, "harbor-team-a"; got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
	if got, want := decision.PhysicalRepository, "production-team-a/service"; got != want {
		t.Fatalf("physical repository = %q, want %q", got, want)
	}
}

func TestResolvePushRejectsDeniedRoute(t *testing.T) {
	engine, err := NewEngine(Config{
		Routes: []Route{
			{Name: "github", Match: "github/**", Precedence: 10, PushDenied: true},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	_, err = engine.ResolvePush(PushRequest{Repository: "github/org/image", Reference: "latest"})
	if err == nil {
		t.Fatal("ResolvePush() error = nil, want denied error")
	}
}
