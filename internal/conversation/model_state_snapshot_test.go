package conversation

import (
	"reflect"
	"sync"
	"testing"
)

func TestAgentModelsSnapshot_NilAndEmpty(t *testing.T) {
	bs := &BackgroundSession{}
	bs.cmSetCurrentModelID("ignored") // Nil model state remains nil.
	if bs.AgentModels() != nil || bs.pdGetAgentModels() != nil || bs.cmHasAgentModels() ||
		bs.cmGetCurrentModelID() != "" || bs.CurrentModelName() != "" {
		t.Fatal("uninitialized model state must remain empty")
	}
	for _, models := range []*SessionModelState{
		{CurrentModelId: "unknown"},
		{AvailableModels: []ModelInfo{}, Synthesized: true},
	} {
		bs.cbStoreAgentModels(models)
		if got := bs.AgentModels(); !reflect.DeepEqual(got, models) {
			t.Fatalf("snapshot = %+v, want %+v (including nil/empty slice)", got, models)
		}
	}
	bs.cbStoreAgentModels(nil)
	if bs.AgentModels() != nil || bs.cmHasAgentModels() {
		t.Fatal("storing nil must clear the catalog")
	}
}

func TestAgentModelsSnapshot_Isolation(t *testing.T) {
	description := "original description"
	input := &SessionModelState{
		CurrentModelId: "model-a",
		AvailableModels: []ModelInfo{
			{ModelId: "model-a", Name: "Model A", Description: &description},
		},
		Synthesized: true,
	}
	bs := &BackgroundSession{}
	bs.cbStoreAgentModels(input)
	snapshot := bs.AgentModels()
	if snapshot == input || !reflect.DeepEqual(snapshot, input) {
		t.Fatal("AgentModels must return an equal, independent snapshot")
	}

	input.CurrentModelId = "caller-only"
	input.AvailableModels[0].Name = "caller-only"
	description = "caller-only"
	input.Synthesized = false
	if got := bs.AgentModels(); !reflect.DeepEqual(got, snapshot) ||
		got.AvailableModels[0].Name != "Model A" || *got.AvailableModels[0].Description != "original description" {
		t.Fatalf("caller mutation leaked into session: %+v", got)
	}

	bs.cmSetCurrentModelID("model-b")
	if snapshot.CurrentModelId != "model-a" || input.CurrentModelId != "caller-only" {
		t.Fatal("session update mutated a retained snapshot or caller state")
	}
	snapshot.CurrentModelId = "snapshot-only"
	snapshot.AvailableModels[0].Name = "snapshot-only"
	*snapshot.AvailableModels[0].Description = "snapshot-only"
	snapshot.Synthesized = false
	got := bs.AgentModels()
	if got.CurrentModelId != "model-b" || got.AvailableModels[0].Name != "Model A" ||
		*got.AvailableModels[0].Description != "original description" || !got.Synthesized {
		t.Fatalf("snapshot mutation leaked into session: %+v", got)
	}

	promptSnapshot := bs.pdGetAgentModels()
	promptSnapshot.CurrentModelId = "prompt-only"
	promptSnapshot.AvailableModels[0].Name = "prompt-only"
	if fresh := bs.AgentModels(); !reflect.DeepEqual(fresh, got) {
		t.Fatalf("prompt snapshot mutation leaked into session: %+v", fresh)
	}
	if bs.CurrentModelName() != "model-b" {
		t.Fatal("unknown current model must fall back to its ID")
	}
}

// Run with -race: catalog replacement and current-model writes must not race
// with any reader, retained snapshots, or startup callers logging their input.
func TestAgentModelsSnapshot_ConcurrentReadersAndWriters(t *testing.T) {
	description := "original description"
	input := &SessionModelState{
		CurrentModelId: "model-a",
		AvailableModels: []ModelInfo{
			{ModelId: "model-a", Name: "Model A", Description: &description},
			{ModelId: "model-b", Name: "Model B"},
		},
		Synthesized: true,
	}
	bs := &BackgroundSession{}
	bs.cbStoreAgentModels(input)
	retained := bs.AgentModels()
	start := make(chan struct{})
	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn()
		}()
	}
	const iterations = 1000
	run(func() {
		for i := 0; i < iterations; i++ {
			bs.cbStoreAgentModels(input)
		}
	})
	run(func() {
		for i := 0; i < iterations; i++ {
			bs.cmSetCurrentModelID("model-b")
			bs.cmSetCurrentModelID("model-a")
		}
	})
	for reader := 0; reader < 4; reader++ {
		run(func() {
			for i := 0; i < iterations; i++ {
				models := bs.AgentModels()
				if i%2 == 0 {
					models = bs.pdGetAgentModels()
				}
				if models == nil || (models.CurrentModelId != "model-a" && models.CurrentModelId != "model-b") ||
					len(models.AvailableModels) != 2 || models.AvailableModels[0].Name != "Model A" ||
					*models.AvailableModels[0].Description != "original description" || !models.Synthesized {
					t.Errorf("invalid snapshot: %+v", models)
					return
				}
				models.CurrentModelId = "reader-only"
				models.AvailableModels[0].Name = "reader-only"
				*models.AvailableModels[0].Description = "reader-only"
				models.Synthesized = false
				if !bs.cmHasAgentModels() {
					t.Error("catalog disappeared")
					return
				}
				if id := bs.cmGetCurrentModelID(); id != "model-a" && id != "model-b" {
					t.Errorf("invalid current model: %q", id)
					return
				}
				if name := bs.CurrentModelName(); name != "Model A" && name != "Model B" {
					t.Errorf("invalid current model name: %q", name)
					return
				}
				if input.CurrentModelId != "model-a" || retained.CurrentModelId != "model-a" ||
					input.AvailableModels[0].Name != "Model A" || retained.AvailableModels[0].Name != "Model A" ||
					*input.AvailableModels[0].Description != "original description" ||
					*retained.AvailableModels[0].Description != "original description" {
					t.Error("session write mutated caller input or retained snapshot")
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()
}
