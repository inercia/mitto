package acpproc

import (
	"testing"

	"github.com/inercia/mitto/internal/conversation"
)

type fakeCatalogProcess struct {
	dir   string
	gen   int
	reads int
}

func (f *fakeCatalogProcess) WorkingDir() string { return f.dir }
func (f *fakeCatalogProcess) Generation() int    { f.reads++; return f.gen }

func newCatalogMgr(got *[]conversation.ModelCatalogObservation) *ACPProcessManager {
	return &ACPProcessManager{ModelCatalogRecorder: func(o conversation.ModelCatalogObservation) error {
		*got = append(*got, o)
		return nil
	}}
}

func TestForwardModelCatalog_ScopeAndComplete(t *testing.T) {
	var got []conversation.ModelCatalogObservation
	m := newCatalogMgr(&got)
	p := &fakeCatalogProcess{dir: "/proj", gen: 3}
	st := &conversation.SessionModelState{AvailableModels: []conversation.ModelInfo{{ModelId: "a"}}}
	m.forwardModelCatalog(p, "srv", 3, st)
	if len(got) != 1 {
		t.Fatalf("got %d observations", len(got))
	}
	o := got[0]
	if o.Server != "srv" || o.WorkingDir != "/proj" || !o.Complete || o.Generation != 3 || len(o.Models) != 1 {
		t.Fatalf("bad observation %+v", o)
	}
	st.AvailableModels[0].ModelId = "mutated"
	if o.Models[0].ModelId != "a" {
		t.Fatal("observation must not alias source")
	}
}

func TestForwardModelCatalog_CompleteEmpty(t *testing.T) {
	var got []conversation.ModelCatalogObservation
	m := newCatalogMgr(&got)
	m.forwardModelCatalog(&fakeCatalogProcess{dir: "/p", gen: 1}, "s", 1, &conversation.SessionModelState{})
	if len(got) != 1 || got[0].Models == nil || len(got[0].Models) != 0 || !got[0].Complete {
		t.Fatalf("want complete-empty, got %+v", got)
	}
}

func TestForwardModelCatalog_StaleGenerationDropped(t *testing.T) {
	var got []conversation.ModelCatalogObservation
	m := newCatalogMgr(&got)
	p := &fakeCatalogProcess{dir: "/p", gen: 4}
	m.forwardModelCatalog(p, "s", 3, &conversation.SessionModelState{AvailableModels: []conversation.ModelInfo{{ModelId: "a"}}})
	if len(got) != 0 {
		t.Fatalf("stale data recorded: %+v", got)
	}
}

func TestForwardModelCatalog_NonObservationsSkipped(t *testing.T) {
	var got []conversation.ModelCatalogObservation
	m := newCatalogMgr(&got)
	p := &fakeCatalogProcess{dir: "/p", gen: 1}
	m.forwardModelCatalog(p, "s", 1, nil)
	m.forwardModelCatalog(p, "s", 1, &conversation.SessionModelState{Synthesized: true, AvailableModels: []conversation.ModelInfo{{ModelId: "x"}}})
	m.forwardModelCatalog(p, "s", 0, &conversation.SessionModelState{})
	(&ACPProcessManager{}).forwardModelCatalog(p, "s", 1, &conversation.SessionModelState{})
	if len(got) != 0 {
		t.Fatalf("unexpected: %+v", got)
	}
	if p.reads != 0 {
		t.Fatalf("process queried for non-observations: %d", p.reads)
	}
}
