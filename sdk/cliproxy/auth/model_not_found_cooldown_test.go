package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// Per-provider positive fixture paired with a generic-404 negative.
func TestIsExplicitModelNotFoundError_ProviderShapes(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		model    string
		explicit bool
	}{
		{"message-only unknown-model", `{"error":"unknown model: gpt-not-a-real-model"}`, "gpt-not-a-real-model", true},
		{"message-only generic 404", `{"error":"Requested entity was not found."}`, "gpt-not-a-real-model", false},
		{"anthropic explicit not-found", `{"type":"error","error":{"type":"not_found_error","message":"model: claude-not-a-real-model"}}`, "claude-not-a-real-model", true},
		{"anthropic generic 404", `{"type":"error","error":{"type":"not_found_error","message":"resource not found"}}`, "claude-not-a-real-model", false},
		{"openai explicit not-found", `{"error":{"message":"The model 'gpt-not-a-real-model' does not exist","type":"invalid_request_error","code":"model_not_found"}}`, "gpt-not-a-real-model", true},
		{"openai generic 404", `{"error":{"message":"Unknown request URL.","type":"invalid_request_error","code":null}}`, "gpt-not-a-real-model", false},
		{"ollama not-found", `{"error":"model \"llama-not-a-real-model\" not found"}`, "llama-not-a-real-model", true},
		{"ollama generic 404", `{"error":"not found"}`, "llama-not-a-real-model", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := statusErrForModelNotFoundTest{code: http.StatusNotFound, msg: tt.body}
			if got := isExplicitModelNotFoundError(err, tt.model); got != tt.explicit {
				t.Fatalf("isExplicitModelNotFoundError(%q, %q) = %v, want %v", tt.body, tt.model, got, tt.explicit)
			}
		})
	}
}

type statusErrForModelNotFoundTest struct {
	code int
	msg  string
}

func (e statusErrForModelNotFoundTest) Error() string   { return e.msg }
func (e statusErrForModelNotFoundTest) StatusCode() int { return e.code }

// An explicit missing-model 404 cools only that model; a sibling model
// stays untouched.
func TestMarkResult_ExplicitModelNotFound_CoolsOnlyTheModel(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	auth := &Auth{ID: "auth-model-404", Provider: "anthropic", Status: StatusActive}
	if _, err := manager.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	manager.MarkResult(context.Background(), Result{
		AuthID: auth.ID, Provider: "anthropic", Model: "claude-real-model", Success: true,
	})
	manager.MarkResult(context.Background(), Result{
		AuthID: auth.ID, Provider: "anthropic", Model: "claude-not-a-real-model", Success: false,
		Error: &Error{
			HTTPStatus: http.StatusNotFound,
			Message:    `{"type":"error","error":{"type":"not_found_error","message":"model: claude-not-a-real-model"}}`,
		},
	})

	updated, ok := manager.GetByID(auth.ID)
	if !ok {
		t.Fatalf("auth %s not found after MarkResult", auth.ID)
	}
	state := updated.ModelStates["claude-not-a-real-model"]
	if state == nil {
		t.Fatal("expected model state for claude-not-a-real-model")
	}
	wantAround := time.Now().Add(12 * time.Hour)
	if state.NextRetryAfter.Before(wantAround.Add(-time.Minute)) || state.NextRetryAfter.After(wantAround.Add(time.Minute)) {
		t.Fatalf("model NextRetryAfter = %v, want ~%v (12h)", state.NextRetryAfter, wantAround)
	}
	if !state.Unavailable {
		t.Fatal("expected model state to be marked unavailable")
	}
	sibling := updated.ModelStates["claude-real-model"]
	if sibling == nil {
		t.Fatal("expected sibling model state for claude-real-model")
	}
	if sibling.Unavailable || !sibling.NextRetryAfter.IsZero() {
		t.Fatalf("sibling model was cooled by another model's 404: Unavailable=%v NextRetryAfter=%v", sibling.Unavailable, sibling.NextRetryAfter)
	}
}

// A non-explicit 404 gets a short retry, not 12h, even with a known model.
func TestMarkResult_GenericModelScoped404_ShortRetryNotTwelveHours(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	auth := &Auth{ID: "auth-generic-404", Provider: "anthropic", Status: StatusActive}
	if _, err := manager.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	manager.MarkResult(context.Background(), Result{
		AuthID: auth.ID, Provider: "anthropic", Model: "gpt-5", Success: false,
		Error: &Error{
			HTTPStatus: http.StatusNotFound,
			Message:    `{"type":"error","error":{"type":"not_found_error","message":"resource not found"}}`,
		},
	})

	updated, ok := manager.GetByID(auth.ID)
	if !ok {
		t.Fatalf("auth %s not found after MarkResult", auth.ID)
	}
	state := updated.ModelStates["gpt-5"]
	if state == nil {
		t.Fatal("expected model state for gpt-5")
	}
	if state.NextRetryAfter.IsZero() {
		t.Fatal("expected a short transient retry window, got zero")
	}
	maxTransient := time.Now().Add(quotaBackoffMax + time.Minute)
	if state.NextRetryAfter.After(maxTransient) {
		t.Fatalf("generic 404 cooled the model for %v, past the transient ceiling %v", state.NextRetryAfter, maxTransient)
	}
}

// The credential-wide path (applyAuthFailureState) must never write a 12h
// credential cooldown for a model-not-found signal. (a) model known: 12h
// goes to that model's state, credential gets short retry. (b) model
// unknown, CODE-signalled: short retry only, no 12h. (c) model unknown,
// structured-BODY-signalled: same as (b).
func TestApplyAuthFailureState_CredentialWidePath_NeverCoolsCredentialForModelNotFound(t *testing.T) {
	codeBody := &Error{HTTPStatus: http.StatusNotFound, Code: "model_not_found", Message: "model not found"}
	structuredBody := &Error{
		HTTPStatus: http.StatusNotFound,
		Message:    `{"type":"error","error":{"type":"not_found_error","message":"model: claude-not-a-real-model"}}`,
	}

	cases := []struct {
		name  string
		err   *Error
		model string
	}{
		{"a_model_known_scopes_to_model_not_credential", codeBody, "claude-not-a-real-model"},
		{"b_model_unknown_code_gets_short_retry_no_12h", codeBody, ""},
		{"c_model_unknown_structured_body_gets_short_retry_no_12h", structuredBody, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			auth := &Auth{ID: "auth-cred-404-" + tc.name}
			applyAuthFailureState(auth, tc.err, nil, tc.model, now, false)

			maxTransient := now.Add(quotaBackoffMax + time.Minute)
			if auth.NextRetryAfter.IsZero() || auth.NextRetryAfter.After(maxTransient) {
				t.Fatalf("credential NextRetryAfter = %v, want a short retry not the 12h", auth.NextRetryAfter)
			}
			if tc.model == "" {
				if len(auth.ModelStates) != 0 {
					t.Fatalf("expected no model state to be created, got %+v", auth.ModelStates)
				}
				return
			}
			want := now.Add(12 * time.Hour)
			state := auth.ModelStates[tc.model]
			if state == nil || !state.NextRetryAfter.Equal(want) || !state.Unavailable {
				t.Fatalf("expected model-scoped 12h cooldown, got state=%+v", state)
			}
		})
	}
}

// Control: a genuine 401 still applies its pre-existing 30m cooldown.
func TestApplyAuthFailureState_GenuineAuth401Unchanged(t *testing.T) {
	now := time.Now()
	auth := &Auth{ID: "auth-401"}
	resultErr := &Error{HTTPStatus: http.StatusUnauthorized, Message: "invalid api key"}

	applyAuthFailureState(auth, resultErr, nil, "", now, false)

	want := now.Add(30 * time.Minute)
	if !auth.NextRetryAfter.Equal(want) {
		t.Fatalf("auth NextRetryAfter = %v, want %v", auth.NextRetryAfter, want)
	}
	if auth.StatusMessage != "unauthorized" {
		t.Fatalf("StatusMessage = %q, want %q", auth.StatusMessage, "unauthorized")
	}
}
