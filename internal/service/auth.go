package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"localshare/internal/domain"
	"localshare/internal/store"
)

const adminUsername = "admin"

type AuthService struct {
	repo   store.Repository
	metric *Metrics
}

func NewAuthService(repo store.Repository, metric *Metrics) *AuthService {
	return &AuthService{repo: repo, metric: metric}
}

func (s *AuthService) SetupRequired(ctx context.Context) bool {
	_, err := s.repo.AdminPasswordHash(ctx, adminUsername)
	return errors.Is(err, domain.ErrNotFound)
}

func (s *AuthService) Setup(ctx context.Context, password string) error {
	if len(password) < 8 {
		return domain.ErrInvalidInput
	}
	if !s.SetupRequired(ctx) {
		return domain.ErrConflict
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	return s.repo.SetAdminPassword(ctx, adminUsername, hash)
}

func (s *AuthService) Login(ctx context.Context, password string) (string, time.Time, error) {
	hash, err := s.repo.AdminPasswordHash(ctx, adminUsername)
	if err != nil {
		return "", time.Time{}, err
	}
	ok, err := verifyPassword(password, hash)
	if err != nil || !ok {
		s.metric.adminFailedLogins.Add(1)
		return "", time.Time{}, domain.ErrUnauthorized
	}
	sessionID, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(24 * time.Hour)
	if err := s.repo.CreateAdminSession(ctx, sessionID, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	s.metric.adminLogins.Add(1)
	return sessionID, expiresAt, nil
}

func (s *AuthService) Session(ctx context.Context, sessionID string) (string, time.Time, error) {
	if sessionID == "" {
		return "", time.Time{}, domain.ErrUnauthorized
	}
	return s.repo.GetAdminSession(ctx, sessionID)
}

func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return s.repo.DeleteAdminSession(ctx, sessionID)
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashPassword(password string) (string, error) {
	const memory = 64 * 1024
	const iterations = 3
	const parallelism = 4
	const keyLen = 32
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, domain.ErrInvalidInput
	}
	params := map[string]int{}
	for _, item := range strings.Split(parts[3], ",") {
		kv := strings.SplitN(item, "=", 2)
		if len(kv) != 2 {
			continue
		}
		value, err := strconv.Atoi(kv[1])
		if err != nil {
			return false, err
		}
		params[kv[0]] = value
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey(
		[]byte(password),
		salt,
		uint32(params["t"]),
		uint32(params["m"]),
		uint8(params["p"]),
		uint32(len(want)),
	)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
