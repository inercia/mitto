package slackcatalog

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/inercia/mitto/internal/secrets"
)

const defaultChannelCacheTTL = time.Minute

var (
	slackAppIDPattern = regexp.MustCompile(`^A[A-Z0-9]{2,}$`)
	teamIDPattern     = regexp.MustCompile(`^[TE][A-Z0-9]{2,}$`)
	botIDPattern      = regexp.MustCompile(`^B[A-Z0-9]{2,}$`)
	userIDPattern     = regexp.MustCompile(`^[UW][A-Z0-9]{2,}$`)
)

type CredentialManager interface {
	Put(secrets.CredentialRef, string) error
	Resolve(secrets.CredentialRef) (string, error)
	Status(secrets.CredentialRef) (secrets.CredentialStatus, error)
	Delete(secrets.CredentialRef) error
}

// ReferenceChecker is implemented by the loop-subscription slice. Keeping the
// interface here lets catalog deletion fail closed without importing session or
// conversation packages.
type ReferenceChecker interface {
	FindSlackReferences(context.Context, string, []string) ([]Reference, error)
}

type noReferences struct{}

func (noReferences) FindSlackReferences(context.Context, string, []string) ([]Reference, error) {
	return []Reference{}, nil
}

type channelCacheEntry struct {
	page      ChannelPage
	expiresAt time.Time
}

type Service struct {
	mu          sync.Mutex
	store       Store
	credentials CredentialManager
	slack       SlackProvider
	references  ReferenceChecker
	now         func() time.Time
	cacheTTL    time.Duration
	channels    map[string]channelCacheEntry
}

func NewService(store Store, credentials CredentialManager, provider SlackProvider, references ReferenceChecker) *Service {
	if references == nil {
		references = noReferences{}
	}
	return &Service{store: store, credentials: credentials, slack: provider, references: references,
		now: time.Now, cacheTTL: defaultChannelCacheTTL, channels: make(map[string]channelCacheEntry)}
}

func (s *Service) ListApps() ([]AppView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	result := make([]AppView, 0, len(doc.Apps))
	for _, app := range doc.Apps {
		view, err := s.appView(app)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *Service) GetApp(id string) (AppView, error) {
	if err := validateResourceID(id); err != nil {
		return AppView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return AppView{}, err
	}
	idx := appIndex(doc, id)
	if idx < 0 {
		return AppView{}, ErrNotFound
	}
	return s.appView(doc.Apps[idx])
}

func (s *Service) CreateApp(ctx context.Context, name, token string) (AppView, error) {
	name, err := validateName(name)
	if err != nil {
		return AppView{}, err
	}
	if s.slack == nil || s.credentials == nil {
		return AppView{}, ErrUnavailable
	}
	slackAppID, err := s.slack.ValidateApp(ctx, token)
	if err != nil {
		return AppView{}, err
	}
	if !slackAppIDPattern.MatchString(slackAppID) {
		return AppView{}, fmt.Errorf("%w: malformed Slack app ID", ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return AppView{}, err
	}
	for _, existing := range doc.Apps {
		if existing.SlackAppID == slackAppID {
			return AppView{}, fmt.Errorf("%w: Slack app already configured", ErrConflict)
		}
	}
	now := s.now().UTC()
	app := AppProfile{ID: uuid.NewString(), Name: name, SlackAppID: slackAppID,
		ValidatedAt: now, CreatedAt: now, UpdatedAt: now}
	doc.Apps = append(doc.Apps, app)
	ref := secrets.SlackAppCredential(app.ID, AppTokenCredential)
	if err := s.commitCredentialAndDocument(ref, token, doc); err != nil {
		return AppView{}, err
	}
	return AppView{AppProfile: app, TokenConfigured: true}, nil
}

func (s *Service) RenameApp(id, name string) (AppView, error) {
	if err := validateResourceID(id); err != nil {
		return AppView{}, err
	}
	name, err := validateName(name)
	if err != nil {
		return AppView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return AppView{}, err
	}
	idx := appIndex(doc, id)
	if idx < 0 {
		return AppView{}, ErrNotFound
	}
	doc.Apps[idx].Name = name
	doc.Apps[idx].UpdatedAt = s.now().UTC()
	if err := s.store.Save(doc); err != nil {
		return AppView{}, err
	}
	return s.appView(doc.Apps[idx])
}

func (s *Service) ReplaceAppToken(ctx context.Context, id, token string) (AppView, error) {
	if err := validateResourceID(id); err != nil {
		return AppView{}, err
	}
	if s.slack == nil || s.credentials == nil {
		return AppView{}, ErrUnavailable
	}
	slackAppID, err := s.slack.ValidateApp(ctx, token)
	if err != nil {
		return AppView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return AppView{}, err
	}
	idx := appIndex(doc, id)
	if idx < 0 {
		return AppView{}, ErrNotFound
	}
	if doc.Apps[idx].SlackAppID != slackAppID {
		return AppView{}, fmt.Errorf("%w: replacement token belongs to a different Slack app", ErrConflict)
	}
	doc.Apps[idx].ValidatedAt = s.now().UTC()
	doc.Apps[idx].UpdatedAt = doc.Apps[idx].ValidatedAt
	if err := s.commitCredentialAndDocument(secrets.SlackAppCredential(id, AppTokenCredential), token, doc); err != nil {
		return AppView{}, err
	}
	return AppView{AppProfile: doc.Apps[idx], TokenConfigured: true}, nil
}

func (s *Service) ValidateApp(ctx context.Context, id string) (AppView, error) {
	if err := validateResourceID(id); err != nil {
		return AppView{}, err
	}
	token, err := s.credentials.Resolve(secrets.SlackAppCredential(id, AppTokenCredential))
	if err != nil {
		return AppView{}, fmt.Errorf("%w: app credential unavailable", ErrUnavailable)
	}
	slackAppID, err := s.slack.ValidateApp(ctx, token)
	if err != nil {
		return AppView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return AppView{}, err
	}
	idx := appIndex(doc, id)
	if idx < 0 {
		return AppView{}, ErrNotFound
	}
	if doc.Apps[idx].SlackAppID != slackAppID {
		return AppView{}, fmt.Errorf("%w: stored token belongs to a different Slack app", ErrConflict)
	}
	doc.Apps[idx].ValidatedAt = s.now().UTC()
	doc.Apps[idx].UpdatedAt = doc.Apps[idx].ValidatedAt
	if err := s.store.Save(doc); err != nil {
		return AppView{}, err
	}
	return AppView{AppProfile: doc.Apps[idx], TokenConfigured: true}, nil
}

func (s *Service) ListInstallations(appID string) ([]InstallationView, error) {
	if err := validateResourceID(appID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	if appIndex(doc, appID) < 0 {
		return nil, ErrNotFound
	}
	result := []InstallationView{}
	for _, installation := range doc.Installations {
		if installation.AppID != appID {
			continue
		}
		view, err := s.installationView(installation)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *Service) GetInstallation(id string) (InstallationView, error) {
	if err := validateResourceID(id); err != nil {
		return InstallationView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return InstallationView{}, err
	}
	idx := installationIndex(doc, id)
	if idx < 0 {
		return InstallationView{}, ErrNotFound
	}
	return s.installationView(doc.Installations[idx])
}

func (s *Service) CreateInstallation(ctx context.Context, appID, name, expectedTeamID, token string) (InstallationView, error) {
	if err := validateResourceID(appID); err != nil {
		return InstallationView{}, err
	}
	name, err := validateName(name)
	if err != nil {
		return InstallationView{}, err
	}
	if expectedTeamID != "" && !teamIDPattern.MatchString(expectedTeamID) {
		return InstallationView{}, fmt.Errorf("%w: malformed expected team ID", ErrInvalid)
	}
	if s.slack == nil || s.credentials == nil {
		return InstallationView{}, ErrUnavailable
	}
	identity, err := s.slack.ValidateInstallation(ctx, token)
	if err != nil {
		return InstallationView{}, err
	}
	if err := validateInstallationIdentity(identity); err != nil {
		return InstallationView{}, err
	}
	if expectedTeamID != "" && identity.TeamID != expectedTeamID {
		return InstallationView{}, fmt.Errorf("%w: token belongs to a different Slack team", ErrConflict)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return InstallationView{}, err
	}
	appIdx := appIndex(doc, appID)
	if appIdx < 0 {
		return InstallationView{}, ErrNotFound
	}
	if doc.Apps[appIdx].SlackAppID != identity.SlackAppID {
		return InstallationView{}, fmt.Errorf("%w: bot token belongs to a different Slack app", ErrConflict)
	}
	for _, existing := range doc.Installations {
		if existing.AppID == appID && existing.TeamID == identity.TeamID {
			return InstallationView{}, fmt.Errorf("%w: workspace already installed for this app", ErrConflict)
		}
	}
	now := s.now().UTC()
	installation := Installation{ID: uuid.NewString(), AppID: appID, Name: name, TeamID: identity.TeamID,
		TeamName: identity.TeamName, BotID: identity.BotID, BotUserID: identity.BotUserID,
		ValidatedAt: now, CreatedAt: now, UpdatedAt: now}
	doc.Installations = append(doc.Installations, installation)
	ref := secrets.SlackInstallationCredential(installation.ID, BotTokenCredential)
	if err := s.commitCredentialAndDocument(ref, token, doc); err != nil {
		return InstallationView{}, err
	}
	return InstallationView{Installation: installation, TokenConfigured: true}, nil
}

func (s *Service) RenameInstallation(id, name string) (InstallationView, error) {
	if err := validateResourceID(id); err != nil {
		return InstallationView{}, err
	}
	name, err := validateName(name)
	if err != nil {
		return InstallationView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return InstallationView{}, err
	}
	idx := installationIndex(doc, id)
	if idx < 0 {
		return InstallationView{}, ErrNotFound
	}
	doc.Installations[idx].Name = name
	doc.Installations[idx].UpdatedAt = s.now().UTC()
	if err := s.store.Save(doc); err != nil {
		return InstallationView{}, err
	}
	return s.installationView(doc.Installations[idx])
}

func (s *Service) ReplaceInstallationToken(ctx context.Context, id, token string) (InstallationView, error) {
	if err := validateResourceID(id); err != nil {
		return InstallationView{}, err
	}
	identity, err := s.slack.ValidateInstallation(ctx, token)
	if err != nil {
		return InstallationView{}, err
	}
	if err := validateInstallationIdentity(identity); err != nil {
		return InstallationView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return InstallationView{}, err
	}
	idx := installationIndex(doc, id)
	if idx < 0 {
		return InstallationView{}, ErrNotFound
	}
	appIdx := appIndex(doc, doc.Installations[idx].AppID)
	if appIdx < 0 {
		return InstallationView{}, fmt.Errorf("%w: parent app missing", ErrConflict)
	}
	if identity.TeamID != doc.Installations[idx].TeamID || identity.SlackAppID != doc.Apps[appIdx].SlackAppID {
		return InstallationView{}, fmt.Errorf("%w: replacement token identity does not match installation", ErrConflict)
	}
	doc.Installations[idx].TeamName = identity.TeamName
	doc.Installations[idx].BotID = identity.BotID
	doc.Installations[idx].BotUserID = identity.BotUserID
	doc.Installations[idx].ValidatedAt = s.now().UTC()
	doc.Installations[idx].UpdatedAt = doc.Installations[idx].ValidatedAt
	if err := s.commitCredentialAndDocument(secrets.SlackInstallationCredential(id, BotTokenCredential), token, doc); err != nil {
		return InstallationView{}, err
	}
	s.invalidateChannelsLocked(id)
	return InstallationView{Installation: doc.Installations[idx], TokenConfigured: true}, nil
}

func (s *Service) ValidateInstallation(ctx context.Context, id string) (InstallationView, error) {
	if err := validateResourceID(id); err != nil {
		return InstallationView{}, err
	}
	token, err := s.credentials.Resolve(secrets.SlackInstallationCredential(id, BotTokenCredential))
	if err != nil {
		return InstallationView{}, fmt.Errorf("%w: installation credential unavailable", ErrUnavailable)
	}
	identity, err := s.slack.ValidateInstallation(ctx, token)
	if err != nil {
		return InstallationView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return InstallationView{}, err
	}
	idx := installationIndex(doc, id)
	if idx < 0 {
		return InstallationView{}, ErrNotFound
	}
	appIdx := appIndex(doc, doc.Installations[idx].AppID)
	if appIdx < 0 || identity.TeamID != doc.Installations[idx].TeamID || identity.SlackAppID != doc.Apps[appIdx].SlackAppID {
		return InstallationView{}, fmt.Errorf("%w: stored token identity does not match installation", ErrConflict)
	}
	doc.Installations[idx].TeamName = identity.TeamName
	doc.Installations[idx].BotID = identity.BotID
	doc.Installations[idx].BotUserID = identity.BotUserID
	doc.Installations[idx].ValidatedAt = s.now().UTC()
	doc.Installations[idx].UpdatedAt = doc.Installations[idx].ValidatedAt
	if err := s.store.Save(doc); err != nil {
		return InstallationView{}, err
	}
	return InstallationView{Installation: doc.Installations[idx], TokenConfigured: true}, nil
}

func (s *Service) PrepareDeleteApp(ctx context.Context, id string) (DeletePreview, error) {
	if err := validateResourceID(id); err != nil {
		return DeletePreview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return DeletePreview{}, err
	}
	if appIndex(doc, id) < 0 {
		return DeletePreview{}, ErrNotFound
	}
	return s.deletePreviewLocked(ctx, doc, id, "")
}

func (s *Service) PrepareDeleteInstallation(ctx context.Context, id string) (DeletePreview, error) {
	if err := validateResourceID(id); err != nil {
		return DeletePreview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return DeletePreview{}, err
	}
	idx := installationIndex(doc, id)
	if idx < 0 {
		return DeletePreview{}, ErrNotFound
	}
	return s.deletePreviewLocked(ctx, doc, doc.Installations[idx].AppID, id)
}

func (s *Service) DeleteApp(ctx context.Context, id string) error {
	if err := validateResourceID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return err
	}
	idx := appIndex(doc, id)
	if idx < 0 {
		return ErrNotFound
	}
	preview, err := s.deletePreviewLocked(ctx, doc, id, "")
	if err != nil {
		return err
	}
	if len(preview.References) > 0 {
		return fmt.Errorf("%w: %d active loop reference(s)", ErrReferenced, len(preview.References))
	}
	refs := []secrets.CredentialRef{secrets.SlackAppCredential(id, AppTokenCredential)}
	for _, installationID := range preview.InstallationIDs {
		refs = append(refs, secrets.SlackInstallationCredential(installationID, BotTokenCredential))
	}
	doc.Apps = append(doc.Apps[:idx], doc.Apps[idx+1:]...)
	installations := doc.Installations[:0]
	for _, installation := range doc.Installations {
		if installation.AppID != id {
			installations = append(installations, installation)
		} else {
			s.invalidateChannelsLocked(installation.ID)
		}
	}
	doc.Installations = installations
	return s.deleteCredentialsAndDocument(refs, doc)
}

func (s *Service) DeleteInstallation(ctx context.Context, id string) error {
	if err := validateResourceID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return err
	}
	idx := installationIndex(doc, id)
	if idx < 0 {
		return ErrNotFound
	}
	preview, err := s.deletePreviewLocked(ctx, doc, doc.Installations[idx].AppID, id)
	if err != nil {
		return err
	}
	if len(preview.References) > 0 {
		return fmt.Errorf("%w: %d active loop reference(s)", ErrReferenced, len(preview.References))
	}
	doc.Installations = append(doc.Installations[:idx], doc.Installations[idx+1:]...)
	s.invalidateChannelsLocked(id)
	return s.deleteCredentialsAndDocument([]secrets.CredentialRef{
		secrets.SlackInstallationCredential(id, BotTokenCredential),
	}, doc)
}

func (s *Service) Channels(ctx context.Context, installationID, cursor string, limit int) (ChannelPage, error) {
	if err := validateResourceID(installationID); err != nil {
		return ChannelPage{}, err
	}
	if limit == 0 {
		limit = DefaultChannelPageSize
	}
	if limit < 1 || limit > MaxChannelPageSize || len(cursor) > 1024 {
		return ChannelPage{}, fmt.Errorf("%w: invalid channel pagination", ErrInvalid)
	}
	cacheKey := fmt.Sprintf("%s\x00%s\x00%d", installationID, cursor, limit)
	s.mu.Lock()
	doc, err := s.store.Load()
	if err != nil {
		s.mu.Unlock()
		return ChannelPage{}, err
	}
	if installationIndex(doc, installationID) < 0 {
		s.mu.Unlock()
		return ChannelPage{}, ErrNotFound
	}
	if entry, ok := s.channels[cacheKey]; ok && s.now().Before(entry.expiresAt) {
		s.mu.Unlock()
		return cloneChannelPage(entry.page), nil
	}
	s.mu.Unlock()
	token, err := s.credentials.Resolve(secrets.SlackInstallationCredential(installationID, BotTokenCredential))
	if err != nil {
		return ChannelPage{}, fmt.Errorf("%w: installation credential unavailable", ErrUnavailable)
	}
	page, err := s.slack.ListPublicChannels(ctx, token, cursor, limit)
	if err != nil {
		return ChannelPage{}, err
	}
	s.mu.Lock()
	s.channels[cacheKey] = channelCacheEntry{page: cloneChannelPage(page), expiresAt: s.now().Add(s.cacheTTL)}
	s.mu.Unlock()
	return page, nil
}

func (s *Service) appView(app AppProfile) (AppView, error) {
	status, err := s.credentials.Status(secrets.SlackAppCredential(app.ID, AppTokenCredential))
	if err != nil {
		return AppView{}, fmt.Errorf("credential status: %w", err)
	}
	return AppView{AppProfile: app, TokenConfigured: status.Configured}, nil
}

func (s *Service) installationView(installation Installation) (InstallationView, error) {
	status, err := s.credentials.Status(secrets.SlackInstallationCredential(installation.ID, BotTokenCredential))
	if err != nil {
		return InstallationView{}, fmt.Errorf("credential status: %w", err)
	}
	return InstallationView{Installation: installation, TokenConfigured: status.Configured}, nil
}

func (s *Service) deletePreviewLocked(ctx context.Context, doc document, appID, onlyInstallationID string) (DeletePreview, error) {
	ids := []string{}
	for _, installation := range doc.Installations {
		if installation.AppID == appID && (onlyInstallationID == "" || installation.ID == onlyInstallationID) {
			ids = append(ids, installation.ID)
		}
	}
	sort.Strings(ids)
	references, err := s.references.FindSlackReferences(ctx, appID, ids)
	if err != nil {
		return DeletePreview{}, fmt.Errorf("%w: reference check failed", ErrUnavailable)
	}
	if references == nil {
		references = []Reference{}
	}
	return DeletePreview{InstallationIDs: ids, References: references}, nil
}

type priorCredential struct {
	ref    secrets.CredentialRef
	value  string
	exists bool
}

func (s *Service) snapshotCredential(ref secrets.CredentialRef) (priorCredential, error) {
	value, err := s.credentials.Resolve(ref)
	if errors.Is(err, secrets.ErrNotFound) {
		return priorCredential{ref: ref}, nil
	}
	if err != nil {
		return priorCredential{}, err
	}
	return priorCredential{ref: ref, value: value, exists: true}, nil
}

func (s *Service) restoreCredential(prior priorCredential) {
	if prior.exists {
		_ = s.credentials.Put(prior.ref, prior.value)
		return
	}
	if err := s.credentials.Delete(prior.ref); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return
	}
}

func (s *Service) commitCredentialAndDocument(ref secrets.CredentialRef, value string, doc document) error {
	prior, err := s.snapshotCredential(ref)
	if err != nil {
		return fmt.Errorf("snapshot credential: %w", err)
	}
	if err := s.credentials.Put(ref, value); err != nil {
		return fmt.Errorf("store credential: %w", err)
	}
	if err := s.store.Save(doc); err != nil {
		s.restoreCredential(prior)
		return err
	}
	return nil
}

func (s *Service) deleteCredentialsAndDocument(refs []secrets.CredentialRef, doc document) error {
	priors := make([]priorCredential, 0, len(refs))
	for _, ref := range refs {
		prior, err := s.snapshotCredential(ref)
		if err != nil {
			return fmt.Errorf("snapshot credential: %w", err)
		}
		priors = append(priors, prior)
	}
	for i, prior := range priors {
		if !prior.exists {
			continue
		}
		if err := s.credentials.Delete(prior.ref); err != nil {
			for j := 0; j < i; j++ {
				s.restoreCredential(priors[j])
			}
			return fmt.Errorf("delete credential: %w", err)
		}
	}
	if err := s.store.Save(doc); err != nil {
		for _, prior := range priors {
			s.restoreCredential(prior)
		}
		return err
	}
	return nil
}

func (s *Service) invalidateChannelsLocked(installationID string) {
	prefix := installationID + "\x00"
	for key := range s.channels {
		if strings.HasPrefix(key, prefix) {
			delete(s.channels, key)
		}
	}
}

func cloneChannelPage(page ChannelPage) ChannelPage {
	clone := ChannelPage{NextCursor: page.NextCursor, Channels: make([]Channel, len(page.Channels))}
	copy(clone.Channels, page.Channels)
	return clone
}

func appIndex(doc document, id string) int {
	for i := range doc.Apps {
		if doc.Apps[i].ID == id {
			return i
		}
	}
	return -1
}

func installationIndex(doc document, id string) int {
	for i := range doc.Installations {
		if doc.Installations[i].ID == id {
			return i
		}
	}
	return -1
}

func validateResourceID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%w: malformed resource ID", ErrInvalid)
	}
	return nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return "", fmt.Errorf("%w: name must contain 1-100 characters", ErrInvalid)
	}
	return name, nil
}

func validateInstallationIdentity(identity InstallationIdentity) error {
	if !slackAppIDPattern.MatchString(identity.SlackAppID) || !teamIDPattern.MatchString(identity.TeamID) ||
		!botIDPattern.MatchString(identity.BotID) || !userIDPattern.MatchString(identity.BotUserID) {
		return fmt.Errorf("%w: Slack returned malformed installation identity", ErrInvalid)
	}
	return nil
}
