package users

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"bbr/internal/token"
)

var (
	ErrEmptyName   = errors.New("user name cannot be empty")
	ErrNameTooLong = errors.New("user name exceeds maximum length")
	ErrNameTaken   = errors.New("user name is already taken")

	ErrInvalidToken  = errors.New("invalid user token")
	ErrInvalidInvite = errors.New("invalid invite token")

	ErrNotFound = errors.New("user not found")
)

const MaxNameLength = 20

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// -- Methods ----

func (s *Service) Create(ctx context.Context, params CreateParams) (*User, string, error) {
	name, err := validateOptionalString(&params.Name, MaxNameLength, ErrEmptyName, ErrNameTooLong)
	if err != nil {
		return nil, "", err
	}
	if name == nil {
		return nil, "", ErrEmptyName
	}

	inviteToken, hash := token.Generate(token.InvitePrefix)

	userParams := CreateParams{
		Name:       *name,
		PrinterIDs: params.PrinterIDs,
		InviteHash: hash,
	}

	user, err := s.store.Create(ctx, userParams)
	if err != nil {
		return nil, "", err
	}

	return user, inviteToken, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]*User, error) {
	if limit <= 0 || limit > 10 {
		limit = 10 // default limit
	}
	if offset < 0 {
		offset = 0 // default offset
	}
	return s.store.List(ctx, limit, offset)
}

func (s *Service) SetPrinters(ctx context.Context, userID uuid.UUID, printerIDs []uuid.UUID) (*User, error) {
	return s.store.SetPrinters(ctx, userID, printerIDs)
}

// -- Authentication and Token Management --

func (s *Service) Authenticate(ctx context.Context, gotToken string) (uuid.UUID, error) {
	if !strings.HasPrefix(gotToken, string(token.SessionPrefix)) {
		return uuid.Nil, ErrInvalidToken
	}

	id, err := s.store.GetUserIDBySessionHash(ctx, token.Hash(gotToken))
	if errors.Is(err, ErrNotFound) {
		return uuid.Nil, ErrInvalidToken
	}
	if err != nil {
		return uuid.Nil, err
	}

	return id, nil
}

func (s *Service) ClaimInvite(ctx context.Context, inviteToken string) (string, error) {
	if !strings.HasPrefix(inviteToken, string(token.InvitePrefix)) {
		return "", ErrInvalidInvite
	}

	sessionToken, sessionHash := token.Generate(token.SessionPrefix)

	_, _, err := s.store.ClaimInvite(ctx, token.Hash(inviteToken), sessionHash)
	if errors.Is(err, ErrNotFound) {
		return "", ErrInvalidInvite
	}
	if err != nil {
		return "", err
	}

	return sessionToken, nil
}

func (s *Service) ReissueInvite(ctx context.Context, id uuid.UUID) (string, error) {
	token, hash := token.Generate(token.InvitePrefix)
	if err := s.store.SetInvite(ctx, id, hash); err != nil {
		return "", err
	}

	return token, nil
}
