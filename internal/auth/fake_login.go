package auth

import (
	"context"
	"errors"
	"sync"
)

// ErrFakeLoginFlowNoResult is returned when a fake login flow has no queued or
// default outcome for a request.
var ErrFakeLoginFlowNoResult = errors.New("fake login flow has no result")

// FakeLoginChallengeOutcome is one queued BeginLogin response.
type FakeLoginChallengeOutcome struct {
	Challenge LoginChallenge
	Err       error
}

// FakeLoginResultOutcome is one queued CompleteLogin response.
type FakeLoginResultOutcome struct {
	Result LoginResult
	Err    error
}

// FakeLoginFlow is an in-memory LoginFlow for unit tests. Recorded requests may
// contain secrets and should not be printed outside tests.
type FakeLoginFlow struct {
	mu sync.Mutex

	beginRequests   []LoginRequest
	completeCalls   []LoginCallback
	beginOutcomes   []FakeLoginChallengeOutcome
	completeResults []FakeLoginResultOutcome
	defaultBegin    FakeLoginChallengeOutcome
	defaultComplete FakeLoginResultOutcome
	hasDefaultBegin bool
	hasDefaultDone  bool
}

var _ LoginFlow = (*FakeLoginFlow)(nil)

// NewFakeLoginFlow creates an empty fake login flow.
func NewFakeLoginFlow() *FakeLoginFlow {
	return &FakeLoginFlow{}
}

// BeginLogin records request and returns the next queued or default challenge.
// It returns ctx.Err before recording when the context is canceled.
func (f *FakeLoginFlow) BeginLogin(ctx context.Context, request LoginRequest) (LoginChallenge, error) {
	if err := ctx.Err(); err != nil {
		return LoginChallenge{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.beginRequests = append(f.beginRequests, cloneLoginRequest(request))
	if len(f.beginOutcomes) > 0 {
		next := f.beginOutcomes[0]
		f.beginOutcomes = f.beginOutcomes[1:]
		return cloneLoginChallenge(next.Challenge), next.Err
	}
	if f.hasDefaultBegin {
		return cloneLoginChallenge(f.defaultBegin.Challenge), f.defaultBegin.Err
	}
	return LoginChallenge{}, ErrFakeLoginFlowNoResult
}

// CompleteLogin records callback and returns the next queued or default result.
// It returns ctx.Err before recording when the context is canceled.
func (f *FakeLoginFlow) CompleteLogin(ctx context.Context, callback LoginCallback) (LoginResult, error) {
	if err := ctx.Err(); err != nil {
		return LoginResult{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.completeCalls = append(f.completeCalls, callback)
	if len(f.completeResults) > 0 {
		next := f.completeResults[0]
		f.completeResults = f.completeResults[1:]
		return cloneLoginResult(next.Result), next.Err
	}
	if f.hasDefaultDone {
		return cloneLoginResult(f.defaultComplete.Result), f.defaultComplete.Err
	}
	return LoginResult{}, ErrFakeLoginFlowNoResult
}

// QueueBegin appends a BeginLogin outcome returned by a future call.
func (f *FakeLoginFlow) QueueBegin(challenge LoginChallenge, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.beginOutcomes = append(f.beginOutcomes, FakeLoginChallengeOutcome{Challenge: cloneLoginChallenge(challenge), Err: err})
}

// QueueComplete appends a CompleteLogin outcome returned by a future call.
func (f *FakeLoginFlow) QueueComplete(result LoginResult, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.completeResults = append(f.completeResults, FakeLoginResultOutcome{Result: cloneLoginResult(result), Err: err})
}

// SetDefaultBegin sets the BeginLogin outcome returned after queued outcomes
// are consumed.
func (f *FakeLoginFlow) SetDefaultBegin(challenge LoginChallenge, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.defaultBegin = FakeLoginChallengeOutcome{Challenge: cloneLoginChallenge(challenge), Err: err}
	f.hasDefaultBegin = true
}

// SetDefaultComplete sets the CompleteLogin outcome returned after queued
// outcomes are consumed.
func (f *FakeLoginFlow) SetDefaultComplete(result LoginResult, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.defaultComplete = FakeLoginResultOutcome{Result: cloneLoginResult(result), Err: err}
	f.hasDefaultDone = true
}

// BeginRequests returns a copy of BeginLogin requests recorded by the fake.
// The returned values may contain secrets and should only be used in tests.
func (f *FakeLoginFlow) BeginRequests() []LoginRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	requests := make([]LoginRequest, len(f.beginRequests))
	for i := range f.beginRequests {
		requests[i] = cloneLoginRequest(f.beginRequests[i])
	}
	return requests
}

// CompleteCalls returns a copy of CompleteLogin callbacks recorded by the fake.
// The returned values may contain secrets and should only be used in tests.
func (f *FakeLoginFlow) CompleteCalls() []LoginCallback {
	f.mu.Lock()
	defer f.mu.Unlock()

	calls := make([]LoginCallback, len(f.completeCalls))
	copy(calls, f.completeCalls)
	return calls
}

func cloneLoginRequest(request LoginRequest) LoginRequest {
	request.Scopes = cloneScopes(request.Scopes)
	return request
}

func cloneLoginChallenge(challenge LoginChallenge) LoginChallenge {
	challenge.Scopes = cloneScopes(challenge.Scopes)
	return challenge
}

func cloneLoginResult(result LoginResult) LoginResult {
	result.Scopes = cloneScopes(result.Scopes)
	result.Tokens = cloneTokenSet(result.Tokens)
	return result
}

func cloneTokenSet(tokens TokenSet) TokenSet {
	tokens.Scopes = cloneScopes(tokens.Scopes)
	return tokens
}
