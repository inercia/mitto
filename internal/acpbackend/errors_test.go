package acpbackend

import (
	"context"
	"errors"
	"fmt"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestTranslateError_Nil(t *testing.T) {
	if err := translateError(nil, ""); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestTranslateError_Cancelled(t *testing.T) {
	err := translateError(context.Canceled, "")
	if !errors.Is(err, agentbackend.ErrCancelled) {
		t.Fatalf("expected ErrCancelled, got %v", err)
	}

	wrapped := fmt.Errorf("prompt failed: %w", context.Canceled)
	if err := translateError(wrapped, ""); !errors.Is(err, agentbackend.ErrCancelled) {
		t.Fatalf("expected ErrCancelled for a wrapped context.Canceled, got %v", err)
	}
}

func TestTranslateError_MethodNotFound(t *testing.T) {
	err := translateError(acp.NewMethodNotFound("session/set_model"), agentbackend.FeatureModelSelection)
	var unsupported *agentbackend.UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *UnsupportedError, got %v (%T)", err, err)
	}
	if unsupported.Feature != agentbackend.FeatureModelSelection {
		t.Errorf("Feature = %q, want %q", unsupported.Feature, agentbackend.FeatureModelSelection)
	}
	if !errors.Is(err, agentbackend.ErrUnsupported) {
		t.Fatalf("expected errors.Is(err, ErrUnsupported) to hold, got %v", err)
	}
}

func TestTranslateError_OtherRequestErrorPassesThrough(t *testing.T) {
	reqErr := &acp.RequestError{Code: -32603, Message: "internal error"}
	err := translateError(reqErr, "")
	if err != reqErr {
		t.Fatalf("expected passthrough of the original *RequestError, got %v (%T)", err, err)
	}
	var unsupported *agentbackend.UnsupportedError
	if errors.As(err, &unsupported) {
		t.Fatalf("did not expect an internal error to be classified as unsupported")
	}
}

func TestTranslateError_UnknownErrorPassesThrough(t *testing.T) {
	plain := errors.New("some ad hoc failure")
	if err := translateError(plain, ""); err != plain {
		t.Fatalf("expected passthrough unchanged, got %v", err)
	}
}
