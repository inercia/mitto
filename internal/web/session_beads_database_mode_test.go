package web

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestPromptBeadsDatabaseModeUsesConfiguredEffectiveMode(t *testing.T) {
	t.Setenv("MITTO_DIR", t.TempDir())
	const workingDir = "/workspace"

	for _, mode := range []config.BeadsDatabaseMode{
		config.BeadsDatabaseModeLocal,
		config.BeadsDatabaseModeShared,
	} {
		t.Run(string(mode), func(t *testing.T) {
			if err := config.SetFolderBeadsDatabaseMode(workingDir, mode); err != nil {
				t.Fatalf("SetFolderBeadsDatabaseMode: %v", err)
			}
			s := &Server{}
			if got := s.promptBeadsDatabaseMode(workingDir); got != mode {
				t.Fatalf("promptBeadsDatabaseMode() = %q, want %q", got, mode)
			}
		})
	}
}
