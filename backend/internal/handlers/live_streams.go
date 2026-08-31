package handlers

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrConcurrentStreamLimit is returned when starting one more live
// transcription stream would exceed the user's plan ceiling.
var ErrConcurrentStreamLimit = errors.New("concurrent transcription limit reached")

// liveTranscriptionStream tracks one active transcription WebSocket. The
// terminate callback is installed after the connection is upgraded; a
// termination requested in the tiny window before that is remembered and
// applied as soon as the callback arrives.
type liveTranscriptionStream struct {
	ConnectionID string
	UserID       string
	TenantID     string
	SessionID    string
	StartedAt    time.Time

	terminate     func(reason string)
	pendingReason string
}

// LiveTranscriptionStreamView is the JSON shape exposed to clients.
type LiveTranscriptionStreamView struct {
	ConnectionID string    `json:"connection_id"`
	SessionID    string    `json:"session_id,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	TenantID     string    `json:"tenant_id,omitempty"`
	StartedAt    time.Time `json:"started_at"`
}

// liveTranscriptionRegistry is the in-process source of truth for live
// transcription streams. Plan concurrency is enforced here — on the live
// connections themselves — rather than on session rows, so a crashed client
// can never occupy a slot after its socket dies.
type liveTranscriptionRegistry struct {
	mu      sync.Mutex
	streams map[string]*liveTranscriptionStream
}

var (
	sharedLiveStreamRegistryOnce sync.Once
	sharedLiveStreamRegistry     *liveTranscriptionRegistry
)

func getSharedLiveTranscriptionRegistry() *liveTranscriptionRegistry {
	sharedLiveStreamRegistryOnce.Do(func() {
		sharedLiveStreamRegistry = newLiveTranscriptionRegistry()
	})
	return sharedLiveStreamRegistry
}

func newLiveTranscriptionRegistry() *liveTranscriptionRegistry {
	return &liveTranscriptionRegistry{streams: make(map[string]*liveTranscriptionStream)}
}

// Acquire registers a stream if the user stays within limit concurrent
// streams (-1 = unlimited). The count and insert are atomic so racing
// connections cannot both slip under the ceiling.
func (r *liveTranscriptionRegistry) Acquire(
	stream *liveTranscriptionStream,
	limit int,
) (func(), error) {
	if r == nil || stream == nil || stream.ConnectionID == "" {
		return func() {}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit >= 0 {
		active := 0
		for _, existing := range r.streams {
			if existing.UserID == stream.UserID {
				active++
			}
		}
		if active >= limit {
			return nil, ErrConcurrentStreamLimit
		}
	}
	if stream.StartedAt.IsZero() {
		stream.StartedAt = time.Now().UTC()
	}
	r.streams[stream.ConnectionID] = stream
	connectionID := stream.ConnectionID
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			delete(r.streams, connectionID)
		})
	}, nil
}

// SetTerminate installs the termination callback for a registered stream and
// immediately applies a termination that raced ahead of the upgrade.
func (r *liveTranscriptionRegistry) SetTerminate(
	connectionID string,
	terminate func(reason string),
) {
	if r == nil {
		return
	}
	r.mu.Lock()
	stream, ok := r.streams[connectionID]
	var fireReason string
	if ok {
		stream.terminate = terminate
		fireReason = stream.pendingReason
		stream.pendingReason = ""
	}
	r.mu.Unlock()
	if ok && fireReason != "" && terminate != nil {
		terminate(fireReason)
	}
}

// CountByUser returns the user's live stream count.
func (r *liveTranscriptionRegistry) CountByUser(userID string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	active := 0
	for _, stream := range r.streams {
		if stream.UserID == userID {
			active++
		}
	}
	return active
}

// ListByUser lists the user's live streams, oldest first. User and tenant ids
// are omitted because the caller already is that user.
func (r *liveTranscriptionRegistry) ListByUser(userID string) []LiveTranscriptionStreamView {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	views := make([]LiveTranscriptionStreamView, 0, 2)
	for _, stream := range r.streams {
		if stream.UserID != userID {
			continue
		}
		views = append(views, LiveTranscriptionStreamView{
			ConnectionID: stream.ConnectionID,
			SessionID:    stream.SessionID,
			StartedAt:    stream.StartedAt,
		})
	}
	sortLiveStreamViews(views)
	return views
}

// ListAll lists every live stream, oldest first, for the admin console.
func (r *liveTranscriptionRegistry) ListAll() []LiveTranscriptionStreamView {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	views := make([]LiveTranscriptionStreamView, 0, len(r.streams))
	for _, stream := range r.streams {
		views = append(views, LiveTranscriptionStreamView{
			ConnectionID: stream.ConnectionID,
			SessionID:    stream.SessionID,
			UserID:       stream.UserID,
			TenantID:     stream.TenantID,
			StartedAt:    stream.StartedAt,
		})
	}
	sortLiveStreamViews(views)
	return views
}

func sortLiveStreamViews(views []LiveTranscriptionStreamView) {
	sort.Slice(views, func(i, j int) bool {
		if views[i].StartedAt.Equal(views[j].StartedAt) {
			return views[i].ConnectionID < views[j].ConnectionID
		}
		return views[i].StartedAt.Before(views[j].StartedAt)
	})
}

// ActiveSessionIDs returns the distinct session ids that currently have a
// live transcription stream attached. The stale-session sweeper treats these
// as untouchable regardless of row timestamps.
func (r *liveTranscriptionRegistry) ActiveSessionIDs() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]struct{}, len(r.streams))
	ids := make([]string, 0, len(r.streams))
	for _, stream := range r.streams {
		if stream.SessionID == "" {
			continue
		}
		if _, ok := seen[stream.SessionID]; ok {
			continue
		}
		seen[stream.SessionID] = struct{}{}
		ids = append(ids, stream.SessionID)
	}
	sort.Strings(ids)
	return ids
}

// Terminate ends one stream. Unless asAdmin is set, the stream must belong to
// requesterID. It reports whether a matching stream existed.
func (r *liveTranscriptionRegistry) Terminate(
	connectionID, requesterID, reason string,
	asAdmin bool,
) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	stream, ok := r.streams[connectionID]
	if ok && !asAdmin && stream.UserID != requesterID {
		ok = false
		stream = nil
	}
	var terminate func(string)
	if ok {
		if stream.terminate != nil {
			terminate = stream.terminate
		} else {
			stream.pendingReason = reason
		}
	}
	r.mu.Unlock()
	if terminate != nil {
		terminate(reason)
	}
	return ok
}

// TerminateBySession ends every live stream the user attached to sessionID
// and returns how many were signaled. Ending a session from another device
// relies on this to actually cut the stream, not just flip the row status.
func (r *liveTranscriptionRegistry) TerminateBySession(
	userID, sessionID, reason string,
) int {
	if r == nil || sessionID == "" {
		return 0
	}
	r.mu.Lock()
	terminations := make([]func(string), 0, 1)
	terminated := 0
	for _, stream := range r.streams {
		if stream.UserID != userID || stream.SessionID != sessionID {
			continue
		}
		terminated++
		if stream.terminate != nil {
			terminations = append(terminations, stream.terminate)
		} else {
			stream.pendingReason = reason
		}
	}
	r.mu.Unlock()
	for _, terminate := range terminations {
		terminate(reason)
	}
	return terminated
}
