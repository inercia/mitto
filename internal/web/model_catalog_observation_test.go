package web

import (
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
)

func TestModelCatalogObservationToCache(t *testing.T) {
	desc := "d"
	at := time.Unix(100, 0)
	got := modelCatalogObservationToCache(conversation.ModelCatalogObservation{
		Server: "srv", WorkingDir: "/w", Complete: true, Generation: 3, ObservedAt: at,
		Models: []conversation.ModelInfo{{ModelId: "a", Name: "A", Description: &desc}, {ModelId: "b", Name: "B"}},
	})
	if got.Server != "srv" || got.WorkingDir != "/w" || !got.Complete || got.Generation != 3 || !got.ObservedAt.Equal(at) {
		t.Fatalf("unexpected observation: %+v", got)
	}
	if len(got.Models) != 2 || got.Models[0].ID != "a" || got.Models[0].Name != "A" || got.Models[0].Description == nil || *got.Models[0].Description != "d" || got.Models[1].Description != nil {
		t.Fatalf("unexpected models: %+v", got.Models)
	}
}
