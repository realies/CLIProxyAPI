package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestResetQuota_UsesAuthIndex(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	manager := coreauth.NewManager(nil, nil, nil)
	next := time.Now().Add(time.Hour)
	auth := &coreauth.Auth{
		ID:             "reset-auth-id",
		FileName:       "reset-auth-file.json",
		Provider:       "claude",
		Status:         coreauth.StatusError,
		StatusMessage:  "quota exhausted",
		Unavailable:    true,
		NextRetryAfter: next,
		Quota:          coreauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: next, BackoffLevel: 2},
		ModelStates: map[string]*coreauth.ModelState{
			"claude-reset-model": {
				Status:         coreauth.StatusError,
				StatusMessage:  "quota exhausted",
				Unavailable:    true,
				NextRetryAfter: next,
				Quota:          coreauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: next, BackoffLevel: 2},
			},
		},
	}
	authIndex := auth.EnsureIndex()
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v0/management/reset-quota", strings.NewReader(`{"auth_index":"`+authIndex+`"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	h.ResetQuota(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("failed to decode response: %v", errUnmarshal)
	}
	if payload["auth_index"] != authIndex {
		t.Fatalf("auth_index = %#v, want %q", payload["auth_index"], authIndex)
	}

	updated, ok := manager.GetByID("reset-auth-id")
	if !ok || updated == nil {
		t.Fatalf("expected auth record to exist after reset")
	}
	if updated.Status != coreauth.StatusActive || updated.StatusMessage != "" || updated.Unavailable || !updated.NextRetryAfter.IsZero() {
		t.Fatalf("updated auth state = status %q message %q unavailable %v next %v", updated.Status, updated.StatusMessage, updated.Unavailable, updated.NextRetryAfter)
	}
	if updated.Quota.Exceeded || updated.Quota.Reason != "" || !updated.Quota.NextRecoverAt.IsZero() || updated.Quota.BackoffLevel != 0 {
		t.Fatalf("updated auth quota = %+v, want cleared", updated.Quota)
	}
	state := updated.ModelStates["claude-reset-model"]
	if state == nil {
		t.Fatalf("expected model state to remain")
	}
	if state.Status != coreauth.StatusActive || state.StatusMessage != "" || state.Unavailable || !state.NextRetryAfter.IsZero() {
		t.Fatalf("updated model state = status %q message %q unavailable %v next %v", state.Status, state.StatusMessage, state.Unavailable, state.NextRetryAfter)
	}
	if state.Quota.Exceeded || state.Quota.Reason != "" || !state.Quota.NextRecoverAt.IsZero() || state.Quota.BackoffLevel != 0 {
		t.Fatalf("updated model quota = %+v, want cleared", state.Quota)
	}
}

func TestResetQuota_DoesNotAcceptAuthIDOrFileName(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "reset-auth-id-only",
		FileName: "reset-auth-file-only.json",
		Provider: "claude",
		Status:   coreauth.StatusError,
	}
	authIndex := auth.EnsureIndex()
	if authIndex == auth.ID || authIndex == auth.FileName {
		t.Fatalf("test auth_index unexpectedly matches id or file name: %q", authIndex)
	}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{name: "auth_id field ignored", body: `{"auth_id":"reset-auth-id-only"}`, wantCode: http.StatusBadRequest},
		{name: "id field ignored", body: `{"id":"reset-auth-id-only"}`, wantCode: http.StatusBadRequest},
		{name: "file name is not an index", body: `{"auth_index":"reset-auth-file-only.json"}`, wantCode: http.StatusNotFound},
		{name: "auth id is not an index", body: `{"auth_index":"reset-auth-id-only"}`, wantCode: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest(http.MethodPost, "/v0/management/reset-quota", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			ctx.Request = req
			h.ResetQuota(ctx)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d with body %s", rec.Code, tt.wantCode, rec.Body.String())
			}
		})
	}
}

// fakeRedeemExecutor is a codex executor that only knows how to redeem; the
// embedded interface satisfies ProviderExecutor for registration.
type fakeRedeemExecutor struct {
	coreauth.ProviderExecutor
	manager       *coreauth.Manager
	err           error
	calls         int
	authID        string
	stillExceeded bool
}

func (f *fakeRedeemExecutor) Identifier() string { return "codex" }

func (f *fakeRedeemExecutor) RedeemQuotaReset(_ context.Context, auth *coreauth.Auth) (*coreauth.QuotaRedeemResult, error) {
	f.calls++
	f.authID = auth.ID
	if current, ok := f.manager.GetByID(auth.ID); ok {
		f.stillExceeded = current.Quota.Exceeded
	}
	if f.err != nil {
		return nil, f.err
	}
	return &coreauth.QuotaRedeemResult{StatusCode: http.StatusOK, Body: json.RawMessage(`{"available_count":1}`)}, nil
}

func TestResetQuota_Redeem(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	tests := []struct {
		name         string
		provider     string
		redeemErr    error
		body         string
		wantCode     int
		wantCalls    int
		wantExceeded bool
	}{
		{name: "redeems then clears", provider: "codex", body: `{"auth_index":%q,"redeem":true}`, wantCode: http.StatusOK, wantCalls: 1},
		{name: "refused redeem keeps cooldown", provider: "codex", redeemErr: errors.New("HTTP 402"), body: `{"auth_index":%q,"redeem":true}`, wantCode: http.StatusBadGateway, wantCalls: 1, wantExceeded: true},
		{name: "unsupported credential kind keeps cooldown", provider: "codex", redeemErr: coreauth.ErrQuotaRedeemUnsupported, body: `{"auth_index":%q,"redeem":true}`, wantCode: http.StatusUnprocessableEntity, wantCalls: 1, wantExceeded: true},
		{name: "provider without executor keeps cooldown", provider: "claude", body: `{"auth_index":%q,"redeem":true}`, wantCode: http.StatusUnprocessableEntity, wantExceeded: true},
		{name: "default never redeems", provider: "codex", body: `{"auth_index":%q}`, wantCode: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := coreauth.NewManager(nil, nil, nil)
			fake := &fakeRedeemExecutor{manager: manager, err: tt.redeemErr}
			manager.RegisterExecutor(fake)
			next := time.Now().Add(time.Hour)
			auth := &coreauth.Auth{
				ID:       "redeem-" + tt.provider,
				FileName: "redeem-" + tt.provider + ".json",
				Provider: tt.provider,
				Status:   coreauth.StatusError,
				Quota:    coreauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: next, BackoffLevel: 2},
			}
			authIndex := auth.EnsureIndex()
			if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
				t.Fatalf("failed to register auth record: %v", errRegister)
			}
			h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest(http.MethodPost, "/v0/management/reset-quota", strings.NewReader(fmt.Sprintf(tt.body, authIndex)))
			req.Header.Set("Content-Type", "application/json")
			ctx.Request = req
			h.ResetQuota(ctx)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d with body %s", rec.Code, tt.wantCode, rec.Body.String())
			}
			if fake.calls != tt.wantCalls {
				t.Fatalf("redeem calls = %d, want %d", fake.calls, tt.wantCalls)
			}
			updated, ok := manager.GetByID(auth.ID)
			if !ok || updated == nil {
				t.Fatalf("expected auth record to exist after reset")
			}
			if updated.Quota.Exceeded != tt.wantExceeded {
				t.Fatalf("quota exceeded = %v, want %v", updated.Quota.Exceeded, tt.wantExceeded)
			}
			if tt.wantCalls == 0 {
				return
			}
			if fake.authID != auth.ID {
				t.Fatalf("redeemed auth = %q, want %q", fake.authID, auth.ID)
			}
			if !fake.stillExceeded {
				t.Fatalf("local cooldown was cleared before the redeem")
			}
			if tt.wantCode != http.StatusOK {
				return
			}
			var payload struct {
				Redeem struct {
					StatusCode int             `json:"status_code"`
					Body       json.RawMessage `json:"body"`
				} `json:"redeem"`
			}
			if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
				t.Fatalf("failed to decode response: %v", errUnmarshal)
			}
			if payload.Redeem.StatusCode != http.StatusOK || string(payload.Redeem.Body) != `{"available_count":1}` {
				t.Fatalf("redeem outcome = %+v, want provider answer passed through", payload.Redeem)
			}
		})
	}
}
