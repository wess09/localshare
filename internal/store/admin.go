package store

import (
	"context"
	"errors"
	"time"

	"localshare/internal/domain"
	"localshare/internal/store/ent"
	"localshare/internal/store/ent/adminsession"
	"localshare/internal/store/ent/adminuser"
	"localshare/internal/store/ent/auditevent"
	"localshare/internal/store/ent/clustersetting"
)

func (s *Store) EnsureAdminUser(ctx context.Context, username, passwordHash string) error {
	_, err := s.client.AdminUser.Query().Where(adminuser.UsernameEQ(username)).Only(ctx)
	if err == nil {
		return nil
	}
	if !ent.IsNotFound(err) {
		return err
	}
	return s.client.AdminUser.Create().SetUsername(username).SetPasswordHash(passwordHash).Exec(ctx)
}

func (s *Store) SetAdminPassword(ctx context.Context, username, passwordHash string) error {
	u, err := s.client.AdminUser.Query().Where(adminuser.UsernameEQ(username)).Only(ctx)
	if ent.IsNotFound(err) {
		return s.client.AdminUser.Create().SetUsername(username).SetPasswordHash(passwordHash).Exec(ctx)
	}
	if err != nil {
		return err
	}
	return u.Update().SetPasswordHash(passwordHash).Exec(ctx)
}

func (s *Store) ValidateAdminPassword(ctx context.Context, username string) (bool, error) {
	_, err := s.client.AdminUser.Query().Where(adminuser.UsernameEQ(username)).Only(ctx)
	if ent.IsNotFound(err) {
		return false, domain.ErrNotFound
	}
	return err == nil, err
}

func (s *Store) AdminPasswordHash(ctx context.Context, username string) (string, error) {
	u, err := s.client.AdminUser.Query().Where(adminuser.UsernameEQ(username)).Only(ctx)
	if ent.IsNotFound(err) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return u.PasswordHash, nil
}

func (s *Store) CreateAdminSession(ctx context.Context, sessionID string, expiresAt time.Time) error {
	return s.client.AdminSession.Create().
		SetSessionID(sessionID).
		SetUsername("admin").
		SetExpiresAt(expiresAt).
		SetLastSeen(time.Now()).
		Exec(ctx)
}

func (s *Store) GetAdminSession(ctx context.Context, sessionID string) (string, time.Time, error) {
	session, err := s.client.AdminSession.Query().Where(adminsession.SessionIDEQ(sessionID)).Only(ctx)
	if ent.IsNotFound(err) {
		return "", time.Time{}, domain.ErrUnauthorized
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if time.Now().After(session.ExpiresAt) {
		_ = s.DeleteAdminSession(ctx, sessionID)
		return "", time.Time{}, domain.ErrUnauthorized
	}
	_ = session.Update().SetLastSeen(time.Now()).Exec(ctx)
	return session.Username, session.ExpiresAt, nil
}

func (s *Store) DeleteAdminSession(ctx context.Context, sessionID string) error {
	_, err := s.client.AdminSession.Delete().Where(adminsession.SessionIDEQ(sessionID)).Exec(ctx)
	return err
}

func (s *Store) CleanupAdminSessions(ctx context.Context, before time.Time) error {
	_, err := s.client.AdminSession.Delete().Where(adminsession.ExpiresAtLT(before)).Exec(ctx)
	return err
}

func (s *Store) UpsertClusterSetting(ctx context.Context, key, value string) error {
	setting, err := s.client.ClusterSetting.Query().Where(clustersetting.KeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return s.client.ClusterSetting.Create().SetKey(key).SetValue(value).Exec(ctx)
	}
	if err != nil {
		return err
	}
	return setting.Update().SetValue(value).Exec(ctx)
}

func (s *Store) GetClusterSetting(ctx context.Context, key string) (string, error) {
	setting, err := s.client.ClusterSetting.Query().Where(clustersetting.KeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (s *Store) ListClusterSettings(ctx context.Context) ([]ClusterSetting, error) {
	settings, err := s.client.ClusterSetting.Query().Order(ent.Asc(clustersetting.FieldKey)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ClusterSetting, 0, len(settings))
	for _, setting := range settings {
		out = append(out, ClusterSetting{
			Key:       setting.Key,
			Value:     setting.Value,
			UpdatedAt: setting.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	events, err := s.client.AuditEvent.Query().Order(ent.Desc(auditevent.FieldCreatedAt)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AuditEvent, 0, len(events))
	for _, event := range events {
		detail := event.Detail
		if detail == nil {
			detail = map[string]any{}
		}
		out = append(out, AuditEvent{
			Actor:     event.Actor,
			Action:    event.Action,
			Target:    event.Target,
			Detail:    detail,
			CreatedAt: event.CreatedAt,
		})
	}
	return out, nil
}

func (s *Store) LogAuditEvent(ctx context.Context, event AuditEvent) error {
	if event.Actor == "" {
		event.Actor = "system"
	}
	if event.Detail == nil {
		event.Detail = map[string]any{}
	}
	if event.Action == "" {
		return errors.New("audit action is required")
	}
	return s.client.AuditEvent.Create().
		SetActor(event.Actor).
		SetAction(event.Action).
		SetTarget(event.Target).
		SetDetail(event.Detail).
		Exec(ctx)
}
