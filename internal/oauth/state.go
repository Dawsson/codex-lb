package oauth

import (
	"context"
	"sort"
	"sync"
	"time"
)

const (
	statusIdle    = "idle"
	statusPending = "pending"
	statusSuccess = "success"
	statusError   = "error"

	methodBrowser = "browser"
	methodDevice  = "device"

	maxRetainedTerminalFlows = 16
	pendingBrowserFlowTTL    = 15 * time.Minute
)

func isTerminalStatus(status string) bool {
	return status == statusSuccess || status == statusError
}

// flow ports the fields of app.modules.oauth.service.OAuthState that are
// relevant to the simplified Go state machine (sync.Mutex + goroutines
// instead of asyncio.Lock/asyncio.Task).
type flow struct {
	FlowID          string
	Status          string
	Method          string
	ErrorMessage    string
	StateToken      string
	CodeVerifier    string
	DeviceAuthID    string
	UserCode        string
	IntervalSeconds int
	ExpiresAt       time.Time
	FinishedAt      time.Time

	pollCancel context.CancelFunc
	pollDone   bool
}

func (f *flow) isPollActive() bool {
	return f.pollCancel != nil && !f.pollDone
}

func (f *flow) clone() flow {
	return flow{
		FlowID:          f.FlowID,
		Status:          f.Status,
		Method:          f.Method,
		ErrorMessage:    f.ErrorMessage,
		StateToken:      f.StateToken,
		CodeVerifier:    f.CodeVerifier,
		DeviceAuthID:    f.DeviceAuthID,
		UserCode:        f.UserCode,
		IntervalSeconds: f.IntervalSeconds,
		ExpiresAt:       f.ExpiresAt,
		FinishedAt:      f.FinishedAt,
	}
}

// stateStore ports app.modules.oauth.service.OAuthStateStore. Callers must
// hold mu while invoking the *Locked methods, mirroring the Python
// "_locked" naming convention for methods that assume the asyncio.Lock is
// held.
type stateStore struct {
	mu sync.Mutex

	latest        flow
	flows         map[string]*flow
	stateTokenIdx map[string]string
}

func newStateStore() *stateStore {
	return &stateStore{
		latest:        flow{Status: statusIdle},
		flows:         make(map[string]*flow),
		stateTokenIdx: make(map[string]string),
	}
}

// getFlowLocked ports OAuthStateStore.get_flow_locked.
func (s *stateStore) getFlowLocked(flowID string) *flow {
	resolved := flowID
	if resolved == "" {
		resolved = s.latest.FlowID
	}
	if resolved == "" {
		return nil
	}
	return s.flows[resolved]
}

// getFlowByStateTokenLocked ports
// OAuthStateStore.get_flow_by_state_token_locked.
func (s *stateStore) getFlowByStateTokenLocked(stateToken string) *flow {
	if stateToken == "" {
		return nil
	}
	flowID, ok := s.stateTokenIdx[stateToken]
	if !ok {
		return nil
	}
	return s.flows[flowID]
}

// rememberFlowLocked ports OAuthStateStore.remember_flow_locked.
func (s *stateStore) rememberFlowLocked(f *flow) {
	s.pruneExpiredPendingBrowserFlowsLocked()
	s.flows[f.FlowID] = f
	if f.StateToken != "" {
		s.stateTokenIdx[f.StateToken] = f.FlowID
	}
	s.setLatestFlowLocked(f)
}

// setLatestFlowLocked ports OAuthStateStore.set_latest_flow_locked.
func (s *stateStore) setLatestFlowLocked(f *flow) {
	s.latest = f.clone()
}

// setFlowStatusLocked ports OAuthStateStore.set_flow_status_locked.
func (s *stateStore) setFlowStatusLocked(f *flow, status string, errorMessage string) {
	f.Status = status
	f.ErrorMessage = errorMessage
	if isTerminalStatus(status) {
		f.FinishedAt = time.Now()
	} else {
		f.FinishedAt = time.Time{}
	}
	s.setLatestFlowLocked(f)
	if isTerminalStatus(status) {
		s.pruneTerminalFlowsLocked()
	}
}

// pruneTerminalFlowsLocked ports OAuthStateStore.prune_terminal_flows_locked.
func (s *stateStore) pruneTerminalFlowsLocked() {
	var terminal []*flow
	for _, f := range s.flows {
		if isTerminalStatus(f.Status) {
			terminal = append(terminal, f)
		}
	}
	extra := len(terminal) - maxRetainedTerminalFlows
	if extra <= 0 {
		return
	}
	sort.Slice(terminal, func(i, j int) bool {
		return terminal[i].FinishedAt.Before(terminal[j].FinishedAt)
	})
	for _, f := range terminal[:extra] {
		s.removeFlowLocked(f)
	}
}

// pruneExpiredPendingBrowserFlowsLocked ports
// OAuthStateStore.prune_expired_pending_browser_flows_locked.
func (s *stateStore) pruneExpiredPendingBrowserFlowsLocked() {
	now := time.Now()
	var expired []*flow
	for _, f := range s.flows {
		if f.Method == methodBrowser && f.Status == statusPending && !f.ExpiresAt.IsZero() && !f.ExpiresAt.After(now) {
			expired = append(expired, f)
		}
	}
	for _, f := range expired {
		s.removeFlowLocked(f)
	}
}

// removePendingDeviceFlowsLocked ports
// OAuthStateStore.remove_pending_device_flows_locked.
func (s *stateStore) removePendingDeviceFlowsLocked() {
	var pending []*flow
	for _, f := range s.flows {
		if f.Method == methodDevice && f.Status == statusPending {
			pending = append(pending, f)
		}
	}
	for _, f := range pending {
		if f.pollCancel != nil {
			f.pollCancel()
		}
		s.removeFlowLocked(f)
	}
}

// removeFlowLocked ports OAuthStateStore.remove_flow_locked.
func (s *stateStore) removeFlowLocked(f *flow) {
	removedLatest := f.FlowID != "" && f.FlowID == s.latest.FlowID
	if f.FlowID != "" {
		delete(s.flows, f.FlowID)
	}
	if f.StateToken != "" {
		delete(s.stateTokenIdx, f.StateToken)
	}
	if removedLatest {
		s.restoreLatestFlowLocked()
	}
}

// restoreLatestFlowLocked ports OAuthStateStore._restore_latest_flow_locked.
func (s *stateStore) restoreLatestFlowLocked() {
	if len(s.flows) == 0 {
		s.latest = flow{Status: statusIdle}
		return
	}
	var latest *flow
	var latestKey time.Time
	for _, f := range s.flows {
		key := f.FinishedAt
		if key.IsZero() {
			key = f.ExpiresAt
		}
		if latest == nil || key.After(latestKey) {
			latest = f
			latestKey = key
		}
	}
	s.setLatestFlowLocked(latest)
}

// hasPendingBrowserFlowsLocked ports
// OAuthStateStore.has_pending_browser_flows_locked.
func (s *stateStore) hasPendingBrowserFlowsLocked() bool {
	s.pruneExpiredPendingBrowserFlowsLocked()
	for _, f := range s.flows {
		if f.Method == methodBrowser && f.Status == statusPending {
			return true
		}
	}
	return false
}
