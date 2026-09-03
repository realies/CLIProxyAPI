package openai

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/tidwall/gjson"
)

func TestApplierEmitsDeclaredResolvedLevelVerbatim(t *testing.T) {
	modelInfo := &registry.ModelInfo{
		Type:        "openai-compatibility",
		UserDefined: true,
		Thinking:    &registry.ThinkingSupport{Levels: []string{"low", "medium", "xhigh"}},
	}
	applier := NewApplier()
	for _, level := range []thinking.ThinkingLevel{thinking.LevelMedium, thinking.LevelXHigh} {
		t.Run(string(level), func(t *testing.T) {
			out, err := applier.Apply(
				[]byte(`{"reasoning_effort":"mutation-sentinel"}`),
				thinking.ThinkingConfig{Mode: thinking.ModeLevel, Level: level},
				modelInfo,
			)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if got := gjson.GetBytes(out, "reasoning_effort").String(); got != string(level) {
				t.Fatalf("reasoning_effort = %q, want resolved level %q", got, level)
			}
		})
	}
}
