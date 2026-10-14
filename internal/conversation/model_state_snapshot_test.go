package conversation

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
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

func TestAgentModelsSnapshot_RecordsOnlyRealCatalogsOutsideLock(t *testing.T) {
	description := "agent description"
	var calls int
	bs := &BackgroundSession{acpServer: "agent-a"}
	bs.onAgentModels = func(acpServer string, models []ModelInfo) error {
		calls++
		if acpServer != "agent-a" {
			t.Errorf("recorded ACP server = %q, want agent-a", acpServer)
		}
		if len(models) != 1 || models[0].ModelId != "model-a" || models[0].Name != "Model A" || models[0].Description == nil || *models[0].Description != "agent description" {
			t.Errorf("recorded catalog = %+v, want only the advertised model", models)
		}

		// A concurrent reader must be able to take the model-state lock while
		// persistence runs; the recorder must not be called under that lock.
		readDone := make(chan struct{})
		go func() {
			_ = bs.AgentModels()
			close(readDone)
		}()
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Error("model-state lock remained held during catalog recording")
		}

		// The callback receives an owned slice; mutating it must not affect the
		// in-memory snapshot. Persistence errors are deliberately non-fatal.
		models[0].Name = "callback mutation"
		*models[0].Description = "callback mutation"
		return errors.New("disk unavailable")
	}

	bs.cbStoreAgentModels(&SessionModelState{
		CurrentModelId: "must-not-be-persisted",
		AvailableModels: []ModelInfo{{
			ModelId:     "model-a",
			Name:        "Model A",
			Description: &description,
		}},
	})
	if calls != 1 {
		t.Fatalf("recorder calls after real catalog = %d, want 1", calls)
	}
	if got := bs.AgentModels(); got == nil || got.CurrentModelId != "must-not-be-persisted" || got.AvailableModels[0].Name != "Model A" || *got.AvailableModels[0].Description != "agent description" {
		t.Fatalf("failed persistence or callback mutation changed in-memory state: %+v", got)
	}

	// Empty and synthesized model states must not erase the last persisted list.
	bs.cbStoreAgentModels(&SessionModelState{AvailableModels: []ModelInfo{}})
	bs.cbStoreAgentModels(&SessionModelState{AvailableModels: []ModelInfo{{ModelId: "local-profile"}}, Synthesized: true})
	bs.cbStoreAgentModels(nil)
	if calls != 1 {
		t.Fatalf("recorder calls after empty/synthetic updates = %d, want 1", calls)
	}
}

func TestResumeBackgroundSession_WiresOnAgentModels(t *testing.T) {
	var gotServer string
	bs, err := ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID:   "test-onagentmodels-resume",
		ACPServer:     "test-server",
		WorkingDir:    "/tmp",
		SharedProcess: newFakeSharedProcess(),
		OnAgentModels: func(acpServer string, _ []ModelInfo) error {
			gotServer = acpServer
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ResumeBackgroundSession failed: %v", err)
	}
	bs.cbStoreAgentModels(&SessionModelState{AvailableModels: []ModelInfo{{ModelId: "m1"}}})
	if gotServer != "test-server" {
		t.Fatalf("recorder not wired on resume; got server %q", gotServer)
	}
}
