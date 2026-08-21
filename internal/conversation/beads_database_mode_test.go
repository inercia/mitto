package conversation

import (
	"context"
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestBackgroundSession_BeadsDatabaseModeResolver(t *testing.T) {
	bs := &BackgroundSession{
		ctx:        context.Background(),
		workingDir: "/workspace",
		beadsDatabaseModeResolver: func(_ context.Context, dir string) (config.BeadsDatabaseMode, error) {
			if dir != "/workspace" {
				t.Fatalf("resolver dir = %q", dir)
			}
			return config.BeadsDatabaseModeShared, nil
		},
	}
	if got := bs.pdBeadsDatabaseMode(); got != config.BeadsDatabaseModeShared {
		t.Fatalf("pdBeadsDatabaseMode() = %q, want shared", got)
	}
}

func TestBackgroundSession_BeadsDatabaseModeResolverFailsLocal(t *testing.T) {
	bs := &BackgroundSession{
		ctx: context.Background(),
		beadsDatabaseModeResolver: func(context.Context, string) (config.BeadsDatabaseMode, error) {
			return "", errors.New("unavailable")
		},
	}
	if got := bs.pdBeadsDatabaseMode(); got != config.BeadsDatabaseModeLocal {
		t.Fatalf("pdBeadsDatabaseMode() = %q, want fail-closed local", got)
	}
}
