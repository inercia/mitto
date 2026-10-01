package conversation

import (
	"reflect"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestResolveInitialModelPreference(t *testing.T) {
	promptPrefs := []config.PromptPreferredModel{{ModelTag: "Reasoning"}}
	fallback := []config.PromptPreferredModel{{ModelTag: "Coding"}}

	tests := []struct {
		name       string
		resolver   func(name, wd string) []config.PromptPreferredModel
		promptName string
		fallback   []config.PromptPreferredModel
		want       []config.PromptPreferredModel
		wantCalls  int
	}{
		{
			name:       "prompt preference precedes fallback",
			resolver:   func(string, string) []config.PromptPreferredModel { return promptPrefs },
			promptName: "p",
			fallback:   fallback,
			want:       append(append([]config.PromptPreferredModel{}, promptPrefs...), fallback...),
			wantCalls:  1,
		},
		{
			name:       "prompt preference alone when no fallback",
			resolver:   func(string, string) []config.PromptPreferredModel { return promptPrefs },
			promptName: "p",
			want:       promptPrefs,
			wantCalls:  1,
		},
		{
			name:       "empty prompt name keeps fallback and skips resolver",
			resolver:   func(string, string) []config.PromptPreferredModel { return promptPrefs },
			promptName: "",
			fallback:   fallback,
			want:       fallback,
			wantCalls:  0,
		},
		{
			name:       "prompt without preference keeps fallback",
			resolver:   func(string, string) []config.PromptPreferredModel { return nil },
			promptName: "p",
			fallback:   fallback,
			want:       fallback,
			wantCalls:  1,
		},
		{
			name:       "no resolver keeps fallback",
			resolver:   nil,
			promptName: "p",
			fallback:   fallback,
			want:       fallback,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewSessionManager("", "test-server", false, nil)
			calls := 0
			var gotName, gotWD string
			if tt.resolver != nil {
				sm.SetPreferredModelsResolver(func(name, wd string) []config.PromptPreferredModel {
					calls++
					gotName, gotWD = name, wd
					return tt.resolver(name, wd)
				})
			}

			got := sm.resolveInitialModelPreference(tt.promptName, "/work", tt.fallback)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("preference = %+v, want %+v", got, tt.want)
			}
			if calls != tt.wantCalls {
				t.Errorf("resolver calls = %d, want %d", calls, tt.wantCalls)
			}
			if calls > 0 && (gotName != tt.promptName || gotWD != "/work") {
				t.Errorf("resolver args = (%q, %q), want (%q, %q)", gotName, gotWD, tt.promptName, "/work")
			}
		})
	}
}

func TestResolveInitialModelPreference_DoesNotMutateFallback(t *testing.T) {
	sm := NewSessionManager("", "test-server", false, nil)
	sm.SetPreferredModelsResolver(func(string, string) []config.PromptPreferredModel {
		return []config.PromptPreferredModel{{ModelTag: "Reasoning"}}
	})
	fallback := make([]config.PromptPreferredModel, 1, 4)
	fallback[0] = config.PromptPreferredModel{ModelTag: "Coding"}

	_ = sm.resolveInitialModelPreference("p", "/work", fallback)
	if len(fallback) != 1 || fallback[0].ModelTag != "Coding" || fallback[:2][1].ModelTag != "" {
		t.Errorf("fallback mutated: %+v", fallback[:2])
	}
}
