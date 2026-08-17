package secrets

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// VaultVersion is the current on-disk/Keychain vault schema version.
const VaultVersion = 1

// NamespaceKind identifies the owner of a credential without exposing its value.
type NamespaceKind string

const (
	NamespaceGlobal            NamespaceKind = "global"
	NamespaceSlackApp          NamespaceKind = "slack-app"
	NamespaceSlackInstallation NamespaceKind = "slack-installation"
)

// CredentialRef identifies one secret in the process-owned vault.
type CredentialRef struct {
	Namespace NamespaceKind
	OwnerID   string
	Name      string
}

// CredentialStatus is safe to serialize: it intentionally contains no value.
type CredentialStatus struct {
	Configured bool `json:"configured"`
}

// BlobBackend persists the complete versioned vault as one opaque document.
type BlobBackend interface {
	Load() ([]byte, error)
	Save([]byte) error
	IsSupported() bool
}

// Manager serializes updates and caches one lazy backend load for its lifetime.
type Manager struct {
	mu      sync.Mutex
	backend BlobBackend
	loaded  bool
	loadErr error
	vault   vaultDocument
}

type vaultDocument struct {
	Version    int                          `json:"version"`
	Namespaces map[string]map[string]string `json:"namespaces"`
}

// NewManager returns an isolated credential manager for backend.
func NewManager(backend BlobBackend) *Manager { return &Manager{backend: backend} }

// GlobalCredential identifies a process-global credential.
func GlobalCredential(name string) CredentialRef {
	return CredentialRef{Namespace: NamespaceGlobal, Name: name}
}

// SlackAppCredential identifies a credential owned by a Slack app profile.
func SlackAppCredential(profileID, name string) CredentialRef {
	return CredentialRef{Namespace: NamespaceSlackApp, OwnerID: profileID, Name: name}
}

// SlackInstallationCredential identifies a credential owned by a Slack installation.
func SlackInstallationCredential(installationID, name string) CredentialRef {
	return CredentialRef{Namespace: NamespaceSlackInstallation, OwnerID: installationID, Name: name}
}

// IsSupported reports whether this manager has a functional secure backend.
func (m *Manager) IsSupported() bool { return m != nil && m.backend != nil && m.backend.IsSupported() }

// Put securely persists value. Failed saves leave the cached vault unchanged.
func (m *Manager) Put(ref CredentialRef, value string) error {
	return m.put(ref, value, false)
}

func (m *Manager) put(ref CredentialRef, value string, verifyPersisted bool) error {
	if err := validateCredentialRef(ref); err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("%w: value is empty", ErrInvalidCredential)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(); err != nil {
		return err
	}
	next := cloneVault(m.vault)
	ns := namespaceKey(ref)
	if next.Namespaces[ns] == nil {
		next.Namespaces[ns] = make(map[string]string)
	}
	next.Namespaces[ns][ref.Name] = value
	if err := m.saveLocked(next); err != nil {
		return err
	}
	if verifyPersisted {
		persisted, err := m.backend.Load()
		if err != nil {
			return fmt.Errorf("verify credential vault write: %w", err)
		}
		verifiedVault, err := decodeVault(persisted)
		if err != nil {
			return fmt.Errorf("verify credential vault write: %w", err)
		}
		verified := verifiedVault.Namespaces[ns][ref.Name]
		if subtle.ConstantTimeCompare([]byte(verified), []byte(value)) != 1 {
			return ErrVerificationFailed
		}
	}
	m.vault = next
	return nil
}

// Resolve returns one secret value or ErrNotFound.
func (m *Manager) Resolve(ref CredentialRef) (string, error) {
	if err := validateCredentialRef(ref); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(); err != nil {
		return "", err
	}
	value, ok := m.vault.Namespaces[namespaceKey(ref)][ref.Name]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

// Status reports only whether a credential is configured.
func (m *Manager) Status(ref CredentialRef) (CredentialStatus, error) {
	if err := validateCredentialRef(ref); err != nil {
		return CredentialStatus{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(); err != nil {
		return CredentialStatus{}, err
	}
	_, ok := m.vault.Namespaces[namespaceKey(ref)][ref.Name]
	return CredentialStatus{Configured: ok}, nil
}

// Delete removes one credential while preserving all unrelated entries.
func (m *Manager) Delete(ref CredentialRef) error {
	if err := validateCredentialRef(ref); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(); err != nil {
		return err
	}
	ns := namespaceKey(ref)
	if _, ok := m.vault.Namespaces[ns][ref.Name]; !ok {
		return ErrNotFound
	}
	next := cloneVault(m.vault)
	delete(next.Namespaces[ns], ref.Name)
	if len(next.Namespaces[ns]) == 0 {
		delete(next.Namespaces, ns)
	}
	if err := m.saveLocked(next); err != nil {
		return err
	}
	m.vault = next
	return nil
}

func (m *Manager) loadLocked() error {
	if m.loaded {
		return m.loadErr
	}
	m.loaded = true
	if !m.IsSupported() {
		m.loadErr = ErrNotSupported
		return m.loadErr
	}
	data, err := m.backend.Load()
	if errors.Is(err, ErrNotFound) {
		m.vault = newVault()
		return nil
	}
	if err != nil {
		m.loadErr = fmt.Errorf("load credential vault: %w", err)
		return m.loadErr
	}
	m.vault, m.loadErr = decodeVault(data)
	return m.loadErr
}

func (m *Manager) saveLocked(v vaultDocument) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode credential vault: %w", err)
	}
	if err := m.backend.Save(data); err != nil {
		return fmt.Errorf("save credential vault: %w", err)
	}
	return nil
}

func decodeVault(data []byte) (vaultDocument, error) {
	var v vaultDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		return vaultDocument{}, fmt.Errorf("%w: invalid JSON", ErrCorruptVault)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return vaultDocument{}, fmt.Errorf("%w: trailing data", ErrCorruptVault)
	}
	if v.Version != VaultVersion {
		return vaultDocument{}, fmt.Errorf("%w: version %d", ErrUnsupportedVaultVersion, v.Version)
	}
	if v.Namespaces == nil {
		return vaultDocument{}, fmt.Errorf("%w: namespaces are missing", ErrCorruptVault)
	}
	return v, nil
}

func newVault() vaultDocument {
	return vaultDocument{Version: VaultVersion, Namespaces: make(map[string]map[string]string)}
}

func cloneVault(v vaultDocument) vaultDocument {
	clone := newVault()
	for namespace, values := range v.Namespaces {
		clone.Namespaces[namespace] = make(map[string]string, len(values))
		for name, value := range values {
			clone.Namespaces[namespace][name] = value
		}
	}
	return clone
}

func namespaceKey(ref CredentialRef) string {
	if ref.Namespace == NamespaceGlobal {
		return string(NamespaceGlobal)
	}
	return string(ref.Namespace) + "/" + ref.OwnerID
}

func validateCredentialRef(ref CredentialRef) error {
	if !validSegment(ref.Name) {
		return fmt.Errorf("%w: invalid credential name", ErrInvalidCredential)
	}
	switch ref.Namespace {
	case NamespaceGlobal:
		if ref.OwnerID != "" {
			return fmt.Errorf("%w: global credentials cannot have an owner", ErrInvalidCredential)
		}
	case NamespaceSlackApp, NamespaceSlackInstallation:
		if !validSegment(ref.OwnerID) {
			return fmt.Errorf("%w: invalid owner ID", ErrInvalidCredential)
		}
	default:
		return fmt.Errorf("%w: unknown namespace", ErrInvalidCredential)
	}
	return nil
}

func validSegment(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}
