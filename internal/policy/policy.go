package policy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

var (
	ErrInvalidRepository = errors.New("invalid repository")
	ErrNoRoute           = errors.New("no matching route")
	ErrPushDenied        = errors.New("push denied by route policy")
)

type Config struct {
	Sources []Source
	Routes  []Route
}

type Source struct {
	ID string
}

type Route struct {
	Name             string
	Match            string
	Precedence       int
	PullSources      []string
	Authoritative    string
	ExternalFallback bool
	PushDestination  string
	PushDenied       bool
	Rewrite          Rewrite
}

type Rewrite struct {
	StripPrefix string
	AddPrefix   string
}

type Engine struct {
	routes []Route
}

type PullRequest struct {
	Repository string
	Reference  string
}

type PullDecision struct {
	RouteName               string
	LogicalRepository       string
	PhysicalRepository      string
	Reference               string
	CandidateSources        []string
	AuthoritativeSource     string
	ExternalFallbackAllowed bool
	Explanation             []string
}

type PushRequest struct {
	Repository string
	Reference  string
}

type PushDecision struct {
	RouteName          string
	LogicalRepository  string
	PhysicalRepository string
	Reference          string
	Destination        string
	Explanation        []string
}

func NewEngine(config Config) (*Engine, error) {
	routes := append([]Route(nil), config.Routes...)
	if err := validateRoutes(config.Sources, routes); err != nil {
		return nil, err
	}

	sort.SliceStable(routes, func(i, j int) bool {
		return routes[i].Precedence < routes[j].Precedence
	})

	return &Engine{routes: routes}, nil
}

func (e *Engine) MatchRoute(repository string) (Route, error) {
	repository, err := NormalizeRepository(repository)
	if err != nil {
		return Route{}, err
	}

	for _, route := range e.routes {
		if matchRepository(route.Match, repository) {
			return route, nil
		}
	}
	return Route{}, ErrNoRoute
}

func (e *Engine) ResolvePull(request PullRequest) (PullDecision, error) {
	logicalRepository, err := NormalizeRepository(request.Repository)
	if err != nil {
		return PullDecision{}, err
	}

	route, err := e.MatchRoute(logicalRepository)
	if err != nil {
		return PullDecision{}, err
	}

	sources := route.PullSources
	if route.Authoritative != "" && !route.ExternalFallback {
		sources = truncateAfter(sources, route.Authoritative)
	}

	physicalRepository := route.Rewrite.Apply(logicalRepository)

	decision := PullDecision{
		RouteName:               route.Name,
		LogicalRepository:       logicalRepository,
		PhysicalRepository:      physicalRepository,
		Reference:               request.Reference,
		CandidateSources:        append([]string(nil), sources...),
		AuthoritativeSource:     route.Authoritative,
		ExternalFallbackAllowed: route.ExternalFallback,
		Explanation: []string{
			fmt.Sprintf("matched route %q", route.Name),
			fmt.Sprintf("rewrote %q to %q", logicalRepository, physicalRepository),
			fmt.Sprintf("candidate pull sources: %s", strings.Join(sources, ", ")),
		},
	}

	if route.Authoritative != "" && !route.ExternalFallback {
		decision.Explanation = append(decision.Explanation, fmt.Sprintf("fallback beyond authoritative source %q is blocked", route.Authoritative))
	}

	return decision, nil
}

func (e *Engine) ResolvePush(request PushRequest) (PushDecision, error) {
	logicalRepository, err := NormalizeRepository(request.Repository)
	if err != nil {
		return PushDecision{}, err
	}

	route, err := e.MatchRoute(logicalRepository)
	if err != nil {
		return PushDecision{}, err
	}
	if route.PushDenied || route.PushDestination == "" {
		return PushDecision{}, fmt.Errorf("%w: %s", ErrPushDenied, route.Name)
	}

	physicalRepository := route.Rewrite.Apply(logicalRepository)
	return PushDecision{
		RouteName:          route.Name,
		LogicalRepository:  logicalRepository,
		PhysicalRepository: physicalRepository,
		Reference:          request.Reference,
		Destination:        route.PushDestination,
		Explanation: []string{
			fmt.Sprintf("matched route %q", route.Name),
			fmt.Sprintf("rewrote %q to %q", logicalRepository, physicalRepository),
			fmt.Sprintf("selected push destination %q", route.PushDestination),
		},
	}, nil
}

func NormalizeRepository(repository string) (string, error) {
	repository = strings.TrimSpace(repository)
	repository = strings.Trim(repository, "/")
	if repository == "" {
		return "", fmt.Errorf("%w: empty repository", ErrInvalidRepository)
	}
	if strings.Contains(repository, "//") {
		return "", fmt.Errorf("%w: repository contains empty path component", ErrInvalidRepository)
	}
	for _, r := range repository {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '/' || r == '.' || r == '_' || r == '-' {
			continue
		}
		return "", fmt.Errorf("%w: repository %q contains unsupported character %q", ErrInvalidRepository, repository, r)
	}
	return repository, nil
}

func (r Rewrite) Apply(repository string) string {
	if r.StripPrefix != "" {
		repository = strings.TrimPrefix(repository, r.StripPrefix)
	}
	if r.AddPrefix != "" {
		repository = r.AddPrefix + repository
	}
	return repository
}

func validateRoutes(sources []Source, routes []Route) error {
	sourceIDs, err := validateSources(sources)
	if err != nil {
		return err
	}

	routeNames := map[string]struct{}{}
	for i, route := range routes {
		if route.Name == "" {
			return fmt.Errorf("route %d has no name", i)
		}
		if _, exists := routeNames[route.Name]; exists {
			return fmt.Errorf("duplicate route name %q", route.Name)
		}
		routeNames[route.Name] = struct{}{}
		if route.Match == "" {
			return fmt.Errorf("route %q has no match pattern", route.Name)
		}
		if _, err := NormalizeRepository(strings.TrimSuffix(route.Match, "/**")); err != nil && route.Match != "**" {
			return fmt.Errorf("route %q has invalid match pattern: %w", route.Name, err)
		}
		if err := validateRouteSources(sourceIDs, route); err != nil {
			return err
		}
		for j := i + 1; j < len(routes); j++ {
			other := routes[j]
			if route.Precedence == other.Precedence && patternsOverlap(route.Match, other.Match) {
				return fmt.Errorf("ambiguous routes %q and %q share precedence %d", route.Name, other.Name, route.Precedence)
			}
		}
	}
	return nil
}

func validateSources(sources []Source) (map[string]struct{}, error) {
	sourceIDs := map[string]struct{}{}
	for i, source := range sources {
		if source.ID == "" {
			return nil, fmt.Errorf("source %d has no id", i)
		}
		if _, exists := sourceIDs[source.ID]; exists {
			return nil, fmt.Errorf("duplicate source id %q", source.ID)
		}
		sourceIDs[source.ID] = struct{}{}
	}
	return sourceIDs, nil
}

func validateRouteSources(sourceIDs map[string]struct{}, route Route) error {
	if len(sourceIDs) == 0 {
		return nil
	}

	for _, sourceID := range route.PullSources {
		if _, exists := sourceIDs[sourceID]; !exists {
			return fmt.Errorf("route %q references unknown pull source %q", route.Name, sourceID)
		}
	}

	if route.Authoritative != "" {
		if _, exists := sourceIDs[route.Authoritative]; !exists {
			return fmt.Errorf("route %q references unknown authoritative source %q", route.Name, route.Authoritative)
		}
		if !contains(route.PullSources, route.Authoritative) {
			return fmt.Errorf("route %q authoritative source %q is not in pull source sequence", route.Name, route.Authoritative)
		}
	}

	if route.PushDestination != "" {
		if _, exists := sourceIDs[route.PushDestination]; !exists {
			return fmt.Errorf("route %q references unknown push destination %q", route.Name, route.PushDestination)
		}
	}

	return nil
}

func matchRepository(pattern, repository string) bool {
	switch {
	case pattern == "**":
		return true
	case strings.HasSuffix(pattern, "/**"):
		prefix := strings.TrimSuffix(pattern, "**")
		return strings.HasPrefix(repository, prefix)
	default:
		return pattern == repository
	}
}

func patternsOverlap(a, b string) bool {
	if a == "**" || b == "**" {
		return true
	}
	if a == b {
		return true
	}
	if strings.HasSuffix(a, "/**") {
		return patternContains(a, b)
	}
	if strings.HasSuffix(b, "/**") {
		return patternContains(b, a)
	}
	return false
}

func patternContains(parent, child string) bool {
	prefix := strings.TrimSuffix(parent, "**")
	if strings.HasSuffix(child, "/**") {
		child = strings.TrimSuffix(child, "**")
	}
	return strings.HasPrefix(child, prefix)
}

func truncateAfter(values []string, terminal string) []string {
	for i, value := range values {
		if value == terminal {
			return append([]string(nil), values[:i+1]...)
		}
	}
	return append([]string(nil), values...)
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
