package slackbridge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/slackcatalog"
)

// LegacyController owns the deprecated listener during an atomic handoff.
type LegacyController interface {
	Active() bool
	Start() error
	Stop() bool
}

// ManagedReconciler activates the newly-persisted canonical subscription.
type ManagedReconciler interface {
	ReconcileSession(string) error
}

// EnvironmentImportRequest contains only user choices; credentials always come
// directly from this process's environment and never cross the API boundary.
type EnvironmentImportRequest struct {
	AppID            string `json:"app_id,omitempty"`
	AppName          string `json:"app_name,omitempty"`
	InstallationID   string `json:"installation_id,omitempty"`
	InstallationName string `json:"installation_name,omitempty"`
}

// EnvironmentImportResult is safe for REST/UI clients.
type EnvironmentImportResult struct {
	slackcatalog.PoCImportResult
	SubscriptionCreated bool `json:"subscription_created"`
	EnvironmentStopped  bool `json:"environment_stopped"`
	ManagedActive       bool `json:"managed_active"`
}

// EnvironmentMigration coordinates catalog, vault, loop, and listener state.
type EnvironmentMigration struct {
	mu         sync.Mutex
	cfg        Config
	baseStatus EnvironmentStatus
	store      *session.Store
	catalog    *slackcatalog.Service
	legacy     LegacyController
	managed    ManagedReconciler
}

func NewEnvironmentMigration(cfg Config, status EnvironmentStatus, store *session.Store, catalog *slackcatalog.Service,
	legacy LegacyController, managed ManagedReconciler) *EnvironmentMigration {
	return &EnvironmentMigration{cfg: cfg, baseStatus: status, store: store, catalog: catalog, legacy: legacy, managed: managed}
}

func (m *EnvironmentMigration) TargetSessionID() string { return m.cfg.TargetSessionID }

// Status returns a fresh value-free precedence view. Persisted subscriptions
// win even when paused, preventing the environment adapter from bypassing them.
func (m *EnvironmentMigration) Status() (EnvironmentStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *EnvironmentMigration) statusLocked() (EnvironmentStatus, error) {
	status := m.baseStatus
	status.Active = m.legacy != nil && m.legacy.Active()
	if !status.Complete || m.store == nil || m.catalog == nil {
		return status, nil
	}
	loop, err := m.store.Loop(m.cfg.TargetSessionID).Get()
	if errors.Is(err, session.ErrLoopNotFound) {
		return status, nil
	}
	if err != nil {
		return EnvironmentStatus{}, err
	}
	if !loop.IsOnSlack() {
		return status, nil
	}
	for _, sub := range loop.SlackSubscriptions {
		if sub.ChannelID != m.cfg.ChannelID {
			continue
		}
		installation, err := m.catalog.GetInstallation(sub.InstallationID)
		if err == nil && installation.TeamID == m.cfg.TeamID {
			status.Shadowed = true
			break
		}
	}
	return status, nil
}

// ReconcileSession keeps ordinary loop edits under the same managed-wins
// precedence as startup and import. A newly persisted matching subscription
// stops the legacy listener before managed routing is activated.
func (m *EnvironmentMigration) ReconcileSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.managed == nil {
		return slackcatalog.ErrUnavailable
	}
	if sessionID != m.cfg.TargetSessionID || !m.baseStatus.Complete {
		return m.managed.ReconcileSession(sessionID)
	}
	status, err := m.statusLocked()
	if err != nil {
		return errors.Join(err, m.managed.ReconcileSession(sessionID))
	}
	if status.Shadowed {
		if m.legacy != nil {
			m.legacy.Stop()
		}
		return m.managed.ReconcileSession(sessionID)
	}
	if err := m.managed.ReconcileSession(sessionID); err != nil {
		return err
	}
	if m.legacy != nil && !m.legacy.Active() {
		return m.legacy.Start()
	}
	return nil
}

// RemoveSession delegates lifecycle cleanup without reviving a legacy listener
// whose target conversation no longer exists.
func (m *EnvironmentMigration) RemoveSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if remover, ok := m.managed.(interface{ RemoveSession(string) }); ok {
		remover.RemoveSession(sessionID)
	}
}

// Import validates every value before mutation, stops the legacy listener,
// persists catalog+loop state, and only then activates managed routing.
func (m *EnvironmentMigration) Import(ctx context.Context, request EnvironmentImportRequest) (EnvironmentImportResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.baseStatus.Complete || m.store == nil || m.catalog == nil || m.managed == nil {
		return EnvironmentImportResult{}, slackcatalog.ErrUnavailable
	}
	if strings.TrimSpace(m.cfg.TargetSessionID) == "" || strings.TrimSpace(m.cfg.ChannelID) == "" {
		return EnvironmentImportResult{}, slackcatalog.ErrInvalid
	}
	loopStore := m.store.Loop(m.cfg.TargetSessionID)
	priorLoop, err := loopStore.Get()
	if err != nil {
		return EnvironmentImportResult{}, fmt.Errorf("%w: target loop unavailable", slackcatalog.ErrConflict)
	}
	nextLoop, err := loopStore.Get()
	if err != nil {
		return EnvironmentImportResult{}, err
	}
	tx, err := m.catalog.PreparePoCImport(ctx, slackcatalog.PoCImportRequest{
		AppID: request.AppID, AppName: request.AppName, InstallationID: request.InstallationID,
		InstallationName: request.InstallationName, ExpectedTeamID: m.cfg.TeamID,
		AppToken: m.cfg.AppToken, BotToken: m.cfg.BotToken,
	})
	if err != nil {
		return EnvironmentImportResult{}, err
	}
	result := EnvironmentImportResult{PoCImportResult: tx.Result()}
	if !slices.Contains(nextLoop.EffectiveTriggers(), session.TriggerOnSlack) {
		nextLoop.Triggers = append(append([]session.LoopTrigger(nil), nextLoop.EffectiveTriggers()...), session.TriggerOnSlack)
	}
	found := false
	for i := range nextLoop.SlackSubscriptions {
		sub := &nextLoop.SlackSubscriptions[i]
		if sub.InstallationID == result.InstallationID && sub.ChannelID == m.cfg.ChannelID {
			sub.EventMode = session.SlackEventModeAnyHumanMessage
			sub.ThreadPolicy = session.SlackThreadPolicyAny
			found = true
			break
		}
	}
	if !found {
		nextLoop.SlackSubscriptions = append(nextLoop.SlackSubscriptions, session.SlackSubscription{
			InstallationID: result.InstallationID, ChannelID: m.cfg.ChannelID,
			EventMode: session.SlackEventModeAnyHumanMessage, ThreadPolicy: session.SlackThreadPolicyAny,
		})
		result.SubscriptionCreated = true
	}

	legacyStopped := m.legacy != nil && m.legacy.Stop()
	result.EnvironmentStopped = legacyStopped
	rollback := func(cause error) (EnvironmentImportResult, error) {
		restoreErr := loopStore.RestoreSnapshot(priorLoop)
		catalogErr := tx.Rollback()
		if legacyStopped {
			if startErr := m.legacy.Start(); startErr != nil {
				restoreErr = errors.Join(restoreErr, startErr)
			}
		}
		return EnvironmentImportResult{}, errors.Join(cause, restoreErr, catalogErr)
	}
	if err := tx.Commit(); err != nil {
		var restartErr error
		if legacyStopped {
			restartErr = m.legacy.Start()
		}
		return EnvironmentImportResult{}, errors.Join(err, restartErr)
	}
	if err := loopStore.Set(nextLoop); err != nil {
		return rollback(err)
	}
	if err := m.managed.ReconcileSession(m.cfg.TargetSessionID); err != nil {
		return rollback(err)
	}
	tx.Finalize()
	result.ManagedActive = nextLoop.Enabled
	return result, nil
}
