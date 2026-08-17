//go:build !darwin && !linux

package secrets

func init() {
	setPlatformStores(&NoopStore{}, &unsupportedBlobBackend{})
}
