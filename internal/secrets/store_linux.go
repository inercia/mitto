//go:build linux

package secrets

import "github.com/inercia/mitto/internal/appdir"

func init() {
	setPlatformStores(&NoopStore{}, newResolvingFileBackend(appdir.CredentialsVaultPath))
}
