package acpproc

import (
	"os"
	"path/filepath"
)

// resolveMittoCLIBinary returns the path of the mitto CLI binary to spawn
// subcommands like `mitto mcp --proxy-to <URL>` from.
//
// When Mitto runs as the native macOS app the current executable is
// `Mitto.app/Contents/MacOS/mitto-app`, which is NOT a cobra CLI: it ignores
// subcommand arguments and always launches the full desktop app (web server,
// up-hook / cloudflared tunnel, webview). Passing that path to an ACP agent as
// the command for a stdio MCP proxy would therefore spawn a whole second Mitto
// app instead of the intended lightweight proxy.
//
// The macOS app bundle always ships the CLI binary next to the app wrapper
// (see Makefile: both are written to `$(APP_BUNDLE)/Contents/MacOS/`), so when
// the current executable is `mitto-app` we prefer the sibling `mitto` binary.
// In every other case (mitto CLI on any OS, test binaries, etc.) we return the
// current executable unchanged.
func resolveMittoCLIBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if filepath.Base(exe) == "mitto-app" {
		sibling := filepath.Join(filepath.Dir(exe), "mitto")
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	return exe, nil
}
