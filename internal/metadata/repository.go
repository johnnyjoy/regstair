package metadata

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrInvalidRecord = errors.New("invalid metadata record")
var ErrConflict = errors.New("metadata conflict")

type Operation string

const (
	OperationPull Operation = "pull"
	OperationPush Operation = "push"
)

type RequestStatus string

const (
	StatusSuccess RequestStatus = "success"
	StatusDenied  RequestStatus = "denied"
	StatusError   RequestStatus = "error"
)

type CacheResult string
type UserAccess string

const (
	CacheHit      CacheResult = "hit"
	CacheMiss     CacheResult = "miss"
	CacheBypassed CacheResult = "bypassed"
)

const (
	UserAccessUser  UserAccess = "user"
	UserAccessAdmin UserAccess = "admin"
)

type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	DisplayName  string     `json:"display_name"`
	Email        string     `json:"email"`
	Access       UserAccess `json:"access"`
	Enabled      bool       `json:"enabled"`
	CreatedAt    time.Time  `json:"ctime"`
	UpdatedAt    time.Time  `json:"mtime"`
}

type RequestEvent struct {
	ID                  int64         `json:"id"`
	Timestamp           time.Time     `json:"timestamp"`
	Operation           Operation     `json:"operation"`
	ClientIdentity      string        `json:"client_identity"`
	CredentialSource    string        `json:"credential_source"`
	LogicalReference    string        `json:"logical_reference"`
	MatchedRoute        string        `json:"matched_route"`
	SourceOrDestination string        `json:"source_or_destination"`
	Status              RequestStatus `json:"status"`
	CacheResult         CacheResult   `json:"cache_result"`
	Duration            time.Duration `json:"duration"`
	BytesTransferred    int64         `json:"bytes_transferred"`
	ErrorClassification string        `json:"error_classification"`
	Explanation         []string      `json:"explanation"`
}

type RequestEventFilter struct {
	ClientIdentity      string
	CredentialSource    string
	Route               string
	Operation           Operation
	SourceOrDestination string
	Status              RequestStatus
	CacheResult         CacheResult
	ErrorClassification string
	ReferenceContains   string
	After               time.Time
	Before              time.Time
}

type RequestEventCursor struct {
	Timestamp time.Time
	ID        int64
}

type RequestEventQuery struct {
	Filter      RequestEventFilter
	Limit       int
	Cursor      *RequestEventCursor
	OldestFirst bool
}

type RequestEventPage struct {
	Events []RequestEvent
	Next   *RequestEventCursor
}

type RequestEventSummary struct {
	Total        int
	Errors       int
	DeniedPushes int
	AuthFailures int
	CacheHits    int
	CacheMisses  int
	Average      time.Duration
	P95          time.Duration
}

type ProvenanceRecord struct {
	LogicalReference        string    `json:"logical_reference"`
	PhysicalSourceReference string    `json:"physical_source_reference"`
	RequestedReference      string    `json:"requested_reference"`
	ResolvedDigest          string    `json:"resolved_digest"`
	Source                  string    `json:"source"`
	Route                   string    `json:"route"`
	FallbackUsed            bool      `json:"fallback_used"`
	StaleServed             bool      `json:"stale_served"`
	RetrievedAt             time.Time `json:"retrieved_at"`
	ValidatedAt             time.Time `json:"validated_at"`
}

type TagMapping struct {
	LogicalRepository string    `json:"logical_repository"`
	Tag               string    `json:"tag"`
	Digest            string    `json:"digest"`
	MediaType         string    `json:"media_type"`
	Size              int64     `json:"size"`
	BlobDigests       []string  `json:"blob_digests"`
	Source            string    `json:"source"`
	Route             string    `json:"route"`
	ResolvedAt        time.Time `json:"resolved_at"`
	LastValidatedAt   time.Time `json:"last_validated_at"`
	FreshUntil        time.Time `json:"fresh_until"`
}

type CacheAccess string

const (
	CacheAccessChallenge           CacheAccess = "challenge"
	CacheAccessProxy               CacheAccess = "proxy"
	CacheAccessCurrentUserRequired CacheAccess = "current_user_required"
)

type CacheBinding struct {
	LogicalRepository  string      `json:"logical_repository"`
	Route              string      `json:"route"`
	Source             string      `json:"source"`
	PhysicalRepository string      `json:"physical_repository"`
	ManifestDigest     string      `json:"manifest_digest"`
	ObjectDigest       string      `json:"object_digest"`
	ObjectKind         string      `json:"object_kind"`
	Access             CacheAccess `json:"access"`
	UserID             string      `json:"user_id,omitempty"`
	CreatedAt          time.Time   `json:"ctime"`
	UpdatedAt          time.Time   `json:"mtime"`
}

type Repository interface {
	RecordRequestEvent(ctx context.Context, event RequestEvent) error
	ListRecentRequestEvents(ctx context.Context, limit int) ([]RequestEvent, error)
	QueryRequestEvents(ctx context.Context, query RequestEventQuery) (RequestEventPage, error)
	CountRequestEvents(ctx context.Context, filter RequestEventFilter) (int, error)
	SummarizeRequestEvents(ctx context.Context, after time.Time) (RequestEventSummary, error)
	FindRequestEventByID(ctx context.Context, id int64) (*RequestEvent, error)
	RecordProvenance(ctx context.Context, record ProvenanceRecord) error
	FindProvenanceByLogicalReference(ctx context.Context, logicalReference string) (*ProvenanceRecord, error)
	RecordTagMapping(ctx context.Context, mapping TagMapping) error
	FindTagMapping(ctx context.Context, logicalRepository string, tag string) (*TagMapping, error)
	ListTagMappings(ctx context.Context) ([]TagMapping, error)
	RecordCacheBindings(ctx context.Context, bindings []CacheBinding) error
	ListCacheBindings(ctx context.Context, logicalRepository, route, objectDigest string) ([]CacheBinding, error)
	CreateUser(ctx context.Context, user User) (*User, error)
	BootstrapAdmin(ctx context.Context, user User, audit AuditEvent) (*User, error)
	FindUserByID(ctx context.Context, id string) (*User, error)
	FindUserByUsername(ctx context.Context, username string) (*User, error)
	ListUsers(ctx context.Context) ([]User, error)
	UpdateUser(ctx context.Context, user User, expectedUpdatedAt time.Time) (*User, error)
}

type MemoryRepository struct {
	mu          sync.RWMutex
	events      []RequestEvent
	provenance  []ProvenanceRecord
	tags        map[string]TagMapping
	bindings    map[string]CacheBinding
	users       map[string]User
	usernames   map[string]string
	now         func() time.Time
	nextEventID int64
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		tags:      map[string]TagMapping{},
		bindings:  map[string]CacheBinding{},
		users:     map[string]User{},
		usernames: map[string]string{},
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func validateUser(user User) error {
	if strings.TrimSpace(user.ID) == "" {
		return fmt.Errorf("%w: user id is required", ErrInvalidRecord)
	}
	if user.Username == "" || user.Username != strings.TrimSpace(user.Username) {
		return fmt.Errorf("%w: username is required and cannot have surrounding whitespace", ErrInvalidRecord)
	}
	if user.PasswordHash == "" {
		return fmt.Errorf("%w: password hash is required", ErrInvalidRecord)
	}
	if user.Access != UserAccessUser && user.Access != UserAccessAdmin {
		return fmt.Errorf("%w: unsupported user access %q", ErrInvalidRecord, user.Access)
	}
	return nil
}

func copyUser(user User) *User {
	copy := user
	return &copy
}

func (r *MemoryRepository) CreateUser(ctx context.Context, user User) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateUser(user); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.users[user.ID]; exists {
		return nil, fmt.Errorf("%w: user id %q", ErrConflict, user.ID)
	}
	if _, exists := r.usernames[user.Username]; exists {
		return nil, fmt.Errorf("%w: username %q", ErrConflict, user.Username)
	}
	now := r.now()
	user.CreatedAt = now
	user.UpdatedAt = now
	r.users[user.ID] = user
	r.usernames[user.Username] = user.ID
	return copyUser(user), nil
}

func (r *MemoryRepository) BootstrapAdmin(ctx context.Context, user User, _ AuditEvent) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateUser(user); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.users) != 0 {
		return nil, fmt.Errorf("%w: administrator bootstrap has already completed", ErrConflict)
	}
	now := r.now()
	user.CreatedAt, user.UpdatedAt = now, now
	r.users[user.ID] = user
	r.usernames[user.Username] = user.ID
	return copyUser(user), nil
}

func (r *MemoryRepository) FindUserByID(ctx context.Context, id string) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	user, ok := r.users[id]
	if !ok {
		return nil, nil
	}
	return copyUser(user), nil
}

func (r *MemoryRepository) FindUserByUsername(ctx context.Context, username string) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.usernames[username]
	if !ok {
		return nil, nil
	}
	return copyUser(r.users[id]), nil
}

func (r *MemoryRepository) ListUsers(ctx context.Context) ([]User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	users := make([]User, 0, len(r.users))
	for _, user := range r.users {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	return users, nil
}

func (r *MemoryRepository) UpdateUser(ctx context.Context, user User, expectedUpdatedAt time.Time) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateUser(user); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.users[user.ID]
	if !ok {
		return nil, nil
	}
	if !current.UpdatedAt.Equal(expectedUpdatedAt) {
		return nil, fmt.Errorf("%w: user %q was modified", ErrConflict, user.ID)
	}
	if owner, exists := r.usernames[user.Username]; exists && owner != user.ID {
		return nil, fmt.Errorf("%w: username %q", ErrConflict, user.Username)
	}
	delete(r.usernames, current.Username)
	user.CreatedAt = current.CreatedAt
	user.UpdatedAt = nextUpdatedAt(r.now(), current.UpdatedAt)
	r.users[user.ID] = user
	r.usernames[user.Username] = user.ID
	return copyUser(user), nil
}

func nextUpdatedAt(now, current time.Time) time.Time {
	if now.After(current) {
		return now
	}
	return current.Add(time.Nanosecond)
}

func (r *MemoryRepository) RecordRequestEvent(ctx context.Context, event RequestEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRequestEvent(event); err != nil {
		return err
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = r.now()
	}
	event.Explanation = append([]string(nil), event.Explanation...)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextEventID++
	event.ID = r.nextEventID
	r.events = append(r.events, event)
	return nil
}

func (r *MemoryRepository) ListRecentRequestEvents(ctx context.Context, limit int) ([]RequestEvent, error) {
	page, err := r.QueryRequestEvents(ctx, RequestEventQuery{Limit: limit})
	return page.Events, err
}

func (r *MemoryRepository) QueryRequestEvents(ctx context.Context, query RequestEventQuery) (RequestEventPage, error) {
	if err := ctx.Err(); err != nil {
		return RequestEventPage{}, err
	}
	if query.Limit < 0 {
		return RequestEventPage{}, fmt.Errorf("%w: negative request event limit", ErrInvalidRecord)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	events := make([]RequestEvent, 0, len(r.events))
	for _, event := range r.events {
		if requestEventMatches(event, query.Filter) && requestEventWithinCursor(event, query.Cursor, query.OldestFirst) {
			events = append(events, event)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Timestamp.Equal(events[j].Timestamp) {
			if query.OldestFirst {
				return events[i].ID < events[j].ID
			}
			return events[i].ID > events[j].ID
		}
		if query.OldestFirst {
			return events[i].Timestamp.Before(events[j].Timestamp)
		}
		return events[i].Timestamp.After(events[j].Timestamp)
	})
	hasMore := query.Limit > 0 && len(events) > query.Limit
	if hasMore {
		events = events[:query.Limit]
	}
	for i := range events {
		events[i].Explanation = append([]string(nil), events[i].Explanation...)
	}
	page := RequestEventPage{Events: events}
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		page.Next = &RequestEventCursor{Timestamp: last.Timestamp, ID: last.ID}
	}
	return page, nil
}

func (r *MemoryRepository) CountRequestEvents(ctx context.Context, filter RequestEventFilter) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, event := range r.events {
		if requestEventMatches(event, filter) {
			count++
		}
	}
	return count, nil
}

func (r *MemoryRepository) SummarizeRequestEvents(ctx context.Context, after time.Time) (RequestEventSummary, error) {
	if err := ctx.Err(); err != nil {
		return RequestEventSummary{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var summary RequestEventSummary
	durations := []time.Duration{}
	for _, event := range r.events {
		if event.Timestamp.Before(after) {
			continue
		}
		summary.Total++
		if event.Status == StatusError {
			summary.Errors++
		}
		if event.Status == StatusDenied && event.Operation == OperationPush {
			summary.DeniedPushes++
		}
		if event.ErrorClassification == "upstream_authentication_failed" {
			summary.AuthFailures++
		}
		if event.CacheResult == CacheHit {
			summary.CacheHits++
		} else if event.CacheResult == CacheMiss {
			summary.CacheMisses++
		}
		if event.Duration > 0 {
			durations = append(durations, event.Duration)
		}
	}
	summary.Average, summary.P95 = summarizeDurations(durations)
	return summary, nil
}

func summarizeDurations(durations []time.Duration) (time.Duration, time.Duration) {
	if len(durations) == 0 {
		return 0, 0
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	var total time.Duration
	for _, duration := range durations {
		total += duration
	}
	index := (95*len(durations) + 99) / 100
	return total / time.Duration(len(durations)), durations[index-1]
}

func (r *MemoryRepository) FindRequestEventByID(ctx context.Context, id int64) (*RequestEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, event := range r.events {
		if event.ID == id {
			value := event
			value.Explanation = append([]string(nil), event.Explanation...)
			return &value, nil
		}
	}
	return nil, nil
}

func requestEventMatches(event RequestEvent, filter RequestEventFilter) bool {
	return (filter.ClientIdentity == "" || event.ClientIdentity == filter.ClientIdentity) &&
		(filter.CredentialSource == "" || event.CredentialSource == filter.CredentialSource) &&
		(filter.Route == "" || event.MatchedRoute == filter.Route) &&
		(filter.Operation == "" || event.Operation == filter.Operation) &&
		(filter.SourceOrDestination == "" || event.SourceOrDestination == filter.SourceOrDestination) &&
		(filter.Status == "" || event.Status == filter.Status) &&
		(filter.CacheResult == "" || event.CacheResult == filter.CacheResult) &&
		(filter.ErrorClassification == "" || event.ErrorClassification == filter.ErrorClassification) &&
		(filter.ReferenceContains == "" || strings.Contains(event.LogicalReference, filter.ReferenceContains)) &&
		(filter.After.IsZero() || !event.Timestamp.Before(filter.After)) &&
		(filter.Before.IsZero() || event.Timestamp.Before(filter.Before))
}

func requestEventWithinCursor(event RequestEvent, cursor *RequestEventCursor, oldestFirst bool) bool {
	if cursor == nil {
		return true
	}
	if oldestFirst {
		return event.Timestamp.After(cursor.Timestamp) || (event.Timestamp.Equal(cursor.Timestamp) && event.ID > cursor.ID)
	}
	return event.Timestamp.Before(cursor.Timestamp) || (event.Timestamp.Equal(cursor.Timestamp) && event.ID < cursor.ID)
}

func (r *MemoryRepository) RecordProvenance(ctx context.Context, record ProvenanceRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateProvenanceRecord(record); err != nil {
		return err
	}
	if record.RetrievedAt.IsZero() {
		record.RetrievedAt = r.now()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.provenance = append(r.provenance, record)
	return nil
}

func (r *MemoryRepository) FindProvenanceByLogicalReference(ctx context.Context, logicalReference string) (*ProvenanceRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var latest *ProvenanceRecord
	for i := range r.provenance {
		record := r.provenance[i]
		if record.LogicalReference != logicalReference {
			continue
		}
		if latest == nil || record.RetrievedAt.After(latest.RetrievedAt) {
			copied := record
			latest = &copied
		}
	}
	return latest, nil
}

func (r *MemoryRepository) RecordTagMapping(ctx context.Context, mapping TagMapping) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTagMapping(mapping); err != nil {
		return err
	}
	if mapping.ResolvedAt.IsZero() {
		mapping.ResolvedAt = r.now()
	}
	if mapping.LastValidatedAt.IsZero() {
		mapping.LastValidatedAt = mapping.ResolvedAt
	}
	mapping.BlobDigests = append([]string(nil), mapping.BlobDigests...)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.tags[tagKey(mapping.LogicalRepository, mapping.Tag)] = mapping
	return nil
}

func (r *MemoryRepository) FindTagMapping(ctx context.Context, logicalRepository string, tag string) (*TagMapping, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	mapping, ok := r.tags[tagKey(logicalRepository, tag)]
	if !ok {
		return nil, nil
	}
	mapping.BlobDigests = append([]string(nil), mapping.BlobDigests...)
	return &mapping, nil
}

func (r *MemoryRepository) ListTagMappings(ctx context.Context) ([]TagMapping, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	mappings := make([]TagMapping, 0, len(r.tags))
	for _, mapping := range r.tags {
		mapping.BlobDigests = append([]string(nil), mapping.BlobDigests...)
		mappings = append(mappings, mapping)
	}
	sort.SliceStable(mappings, func(i, j int) bool {
		if mappings[i].LogicalRepository == mappings[j].LogicalRepository {
			return mappings[i].Tag < mappings[j].Tag
		}
		return mappings[i].LogicalRepository < mappings[j].LogicalRepository
	})
	return mappings, nil
}

func validateRequestEvent(event RequestEvent) error {
	if event.Operation == "" {
		return fmt.Errorf("%w: request event has no operation", ErrInvalidRecord)
	}
	if event.LogicalReference == "" {
		return fmt.Errorf("%w: request event has no logical reference", ErrInvalidRecord)
	}
	if event.Status == "" {
		return fmt.Errorf("%w: request event has no status", ErrInvalidRecord)
	}
	return nil
}

func validateTagMapping(mapping TagMapping) error {
	if mapping.LogicalRepository == "" {
		return fmt.Errorf("%w: tag mapping has no logical repository", ErrInvalidRecord)
	}
	if mapping.Tag == "" {
		return fmt.Errorf("%w: tag mapping has no tag", ErrInvalidRecord)
	}
	if mapping.Digest == "" {
		return fmt.Errorf("%w: tag mapping has no digest", ErrInvalidRecord)
	}
	if mapping.MediaType == "" {
		return fmt.Errorf("%w: tag mapping has no media type", ErrInvalidRecord)
	}
	if mapping.Source == "" {
		return fmt.Errorf("%w: tag mapping has no source", ErrInvalidRecord)
	}
	if mapping.Route == "" {
		return fmt.Errorf("%w: tag mapping has no route", ErrInvalidRecord)
	}
	if mapping.Size < 0 {
		return fmt.Errorf("%w: tag mapping has negative size", ErrInvalidRecord)
	}
	return nil
}

func tagKey(logicalRepository string, tag string) string {
	return logicalRepository + ":" + tag
}

func validateProvenanceRecord(record ProvenanceRecord) error {
	if record.LogicalReference == "" {
		return fmt.Errorf("%w: provenance has no logical reference", ErrInvalidRecord)
	}
	if record.RequestedReference == "" {
		return fmt.Errorf("%w: provenance has no requested reference", ErrInvalidRecord)
	}
	if record.ResolvedDigest == "" {
		return fmt.Errorf("%w: provenance has no resolved digest", ErrInvalidRecord)
	}
	if record.Source == "" {
		return fmt.Errorf("%w: provenance has no source", ErrInvalidRecord)
	}
	if record.Route == "" {
		return fmt.Errorf("%w: provenance has no route", ErrInvalidRecord)
	}
	return nil
}
