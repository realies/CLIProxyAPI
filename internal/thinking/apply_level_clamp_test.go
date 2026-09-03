package thinking

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/tidwall/gjson"
)

func TestMapConfiguredHighIntentClampsUndeclaredLevel(t *testing.T) {
	modelInfo := &registry.ModelInfo{
		Type:     "openai-compatibility",
		Thinking: &registry.ThinkingSupport{Levels: []string{"low", "medium", "xhigh"}},
	}
	tests := []struct {
		request ThinkingLevel
		want    ThinkingLevel
	}{
		{request: LevelMinimal, want: LevelLow},
		{request: LevelLow, want: LevelLow},
		{request: LevelMedium, want: LevelMedium},
		{request: LevelHigh, want: LevelMedium},
		{request: LevelXHigh, want: LevelXHigh},
		{request: LevelMax, want: LevelXHigh},
	}
	for _, tc := range tests {
		t.Run(string(tc.request), func(t *testing.T) {
			if got := mapConfiguredHighIntent(tc.request, modelInfo); got != tc.want {
				t.Fatalf("mapConfiguredHighIntent(%q) = %q, want %q", tc.request, got, tc.want)
			}
		})
	}
}

func TestMapConfiguredHighIntentWithoutDeclaredLevelsIsUnchanged(t *testing.T) {
	modelInfo := &registry.ModelInfo{Type: "openai-compatibility", Thinking: &registry.ThinkingSupport{}}
	if got := mapConfiguredHighIntent(LevelHigh, modelInfo); got != LevelHigh {
		t.Fatalf("mapConfiguredHighIntent(%q) = %q, want unchanged", LevelHigh, got)
	}
}

func TestApplyThinkingWithDeclaredCompatibilityLevelsEmitsResolvedLevelVerbatim(t *testing.T) {
	modelInfo := &registry.ModelInfo{
		ID:          "compat-upstream",
		Type:        "openai-compatibility",
		UserDefined: true,
		Thinking:    &registry.ThinkingSupport{Levels: []string{"low", "medium", "xhigh"}},
	}
	tests := []struct {
		request ThinkingLevel
		want    string
	}{
		{request: LevelXHigh, want: "xhigh"},
		{request: LevelMax, want: "xhigh"},
		{request: LevelHigh, want: "medium"},
	}
	for _, tc := range tests {
		t.Run(string(tc.request), func(t *testing.T) {
			body := []byte(`{"reasoning_effort":"` + string(tc.request) + `"}`)
			out, err := ApplyThinkingWithModelInfo(body, body, modelInfo.ID, "openai", "openai", "compat-provider", modelInfo)
			if err != nil {
				t.Fatalf("ApplyThinkingWithModelInfo() error = %v", err)
			}
			if got := gjson.GetBytes(out, "reasoning_effort").String(); got != tc.want {
				t.Fatalf("reasoning_effort = %q, want %q; body=%s", got, tc.want, out)
			}
		})
	}
}

func TestApplyThinkingWithoutDeclaredCompatibilityLevelsIsUnchanged(t *testing.T) {
	modelInfo := &registry.ModelInfo{
		ID:          "compat-upstream",
		Type:        "openai-compatibility",
		UserDefined: true,
		Thinking:    &registry.ThinkingSupport{},
	}
	body := []byte(`{"reasoning_effort":"xhigh"}`)
	out, err := ApplyThinkingWithModelInfo(body, body, modelInfo.ID, "openai", "openai", "compat-provider", modelInfo)
	if err != nil {
		t.Fatalf("ApplyThinkingWithModelInfo() error = %v", err)
	}
	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "xhigh" {
		t.Fatalf("reasoning_effort = %q, want unchanged xhigh; body=%s", got, out)
	}
}

func TestApplyThinkingDeclaredLevelsMutationControl(t *testing.T) {
	body := []byte(`{"reasoning_effort":"xhigh"}`)
	apply := func(levels []string) string {
		t.Helper()
		modelInfo := &registry.ModelInfo{
			ID:          "compat-upstream",
			Type:        "openai-compatibility",
			UserDefined: true,
			Thinking:    &registry.ThinkingSupport{Levels: levels},
		}
		out, err := ApplyThinkingWithModelInfo(body, body, modelInfo.ID, "openai", "openai", "compat-provider", modelInfo)
		if err != nil {
			t.Fatalf("ApplyThinkingWithModelInfo() error = %v", err)
		}
		return gjson.GetBytes(out, "reasoning_effort").String()
	}
	if got := apply([]string{"low", "medium", "xhigh"}); got != "xhigh" {
		t.Fatalf("declared xhigh effort = %q, want xhigh", got)
	}
	if got := apply([]string{"low", "medium", "high"}); got != "high" {
		t.Fatalf("mutated declarations effort = %q, want high", got)
	}
}

func TestApplyThinkingWithDeclaredCompatibilityLevelsAvoidsBudgetRoundTrip(t *testing.T) {
	modelInfo := &registry.ModelInfo{
		ID:          "compat-upstream",
		Type:        "openai-compatibility",
		UserDefined: true,
		Thinking:    &registry.ThinkingSupport{Levels: []string{"low", "medium", "xhigh"}},
	}
	body := []byte(`{"reasoning_effort":"max"}`)
	source := []byte(`{"thinking":{"type":"enabled","budget_tokens":32768}}`)
	out, err := ApplyThinkingWithModelInfo(body, source, modelInfo.ID, "claude", "openai", "compat-provider", modelInfo)
	if err != nil {
		t.Fatalf("ApplyThinkingWithModelInfo() error = %v", err)
	}
	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "xhigh" {
		t.Fatalf("reasoning_effort = %q, want declared xhigh instead of budget-derived max; body=%s", got, out)
	}
}
