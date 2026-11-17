package users

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"bbr/internal/token"
	"bbr/internal/validate"
)

var (
	ErrEmptyUserName   = errors.New("user name cannot be empty")
	ErrUserNameTooLong = errors.New("user name exceeds maximum length")

	ErrEmptyDeviceName   = errors.New("device name cannot be empty")
	ErrDeviceNameTooLong = errors.New("device name exceeds maximum length")

	ErrInvalidRole = errors.New("invalid role")

	ErrInvalidToken  = errors.New("invalid user token")
	ErrInvalidInvite = errors.New("invalid invite token")

	ErrPrinterOwnershipViolation = errors.New("user already owns a printer or the printer is already owned by another user")
	ErrConflictingRoles          = errors.New("conflicting roles for the same printer")

	ErrUserNotFound    = errors.New("user not found")
	ErrMemberNotFound  = errors.New("member not found")
	ErrSessionNotFound = errors.New("session not found")
	ErrUnknownPrinter  = errors.New("unknown printer ID")
)

const (
	MaxUserNameLength   = 20
	MaxDeviceNameLength = 50
	InviteExpiryTimeout = 2 * 24 * time.Hour
)

type SubmitParams struct {
	Name       string
	PrinterIDs []uuid.UUID
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// -- Methods ----

func (s *Service) Create(ctx context.Context, params SubmitParams) (*User, string, error) {
	name, err := validate.RequiredString(params.Name, MaxUserNameLength, ErrEmptyUserName, ErrUserNameTooLong, false)
	if err != nil {
		return nil, "", err
	}

	inviteToken, hash := token.Generate(token.InvitePrefix)

	// Set role to Member and remove duplicates
	printerAccesses := make([]PrinterAccess, len(params.PrinterIDs))
	for i, printerID := range params.PrinterIDs {
		printerAccesses[i] = PrinterAccess{
			PrinterID: printerID,
			Role:      RoleMember, // default role for new users
		}
	}
	dedupedPrinterAccesses, err := deconflictPrinterAccesses(printerAccesses)
	if err != nil {
		return nil, "", err
	}

	userParams := CreateParams{
		Name:            name,
		PrinterAccesses: dedupedPrinterAccesses,
		InviteHash:      hash,
		InviteExpiresAt: time.Now().Add(InviteExpiryTimeout),
	}

	user, err := s.store.Create(ctx, userParams)
	if err != nil {
		return nil, "", err
	}

	return user, inviteToken, nil
}

func (s *Service) AddMember(ctx context.Context, printerID uuid.UUID, name string) (*User, string, error) {
	name, err := validate.RequiredString(name, MaxUserNameLength, ErrEmptyUserName, ErrUserNameTooLong, false)
	if err != nil {
		return nil, "", err
	}

	inviteToken, hash := token.Generate(token.InvitePrefix)

	userParams := CreateParams{
		Name: name,
		PrinterAccesses: []PrinterAccess{
			{
				PrinterID: printerID,
				Role:      RoleMember,
			},
		},
		InviteHash:      hash,
		InviteExpiresAt: time.Now().Add(InviteExpiryTimeout),
	}

	user, err := s.store.Create(ctx, userParams)
	if err != nil {
		return nil, "", err
	}

	// Return the list of members for the printer after adding the new member.
	return user, inviteToken, nil
}

func (s *Service) RemoveMember(ctx context.Context, printerID, userID uuid.UUID) error {
	return s.store.RemoveMember(ctx, printerID, userID)
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

func (s *Service) ListMembers(ctx context.Context, printerID uuid.UUID) ([]*User, error) {
	return s.store.ListMembers(ctx, printerID)
}

func (s *Service) ListSessions(ctx context.Context, userID uuid.UUID) ([]*Session, error) {
	return s.store.ListSessions(ctx, userID)
}

func (s *Service) SetPrinters(ctx context.Context, userID uuid.UUID, printerAccesses []PrinterAccess) (*User, error) {
	for _, pa := range printerAccesses {
		if !pa.Role.IsValid() {
			return nil, fmt.Errorf("%w: %q", ErrInvalidRole, pa.Role)
		}
	}

	// Remove duplicates and conflicts to avoid unique constraint violations in the database.
	dedupedPrinterAccesses, err := deconflictPrinterAccesses(printerAccesses)
	if err != nil {
		return nil, err
	}

	// Empty printerAccesses removes all printer accesses
	return s.store.SetPrinters(ctx, userID, dedupedPrinterAccesses)
}

func (s *Service) SetOwner(ctx context.Context, userID, printerID uuid.UUID) (*User, error) {
	return s.store.SetOwner(ctx, userID, printerID)
}

func (s *Service) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	return s.store.RevokeSession(ctx, userID, sessionID)
}

func (s *Service) RevokeAllSessions(ctx context.Context, userID uuid.UUID) error {
	return s.store.RevokeAllSessions(ctx, userID)
}

func (s *Service) CanSend(ctx context.Context, userID, printerID uuid.UUID) (bool, error) {
	return s.store.CanSend(ctx, userID, printerID)
}

func (s *Service) IsOwner(ctx context.Context, userID, printerID uuid.UUID) (bool, error) {
	return s.store.IsOwner(ctx, userID, printerID)
}

// -- Authentication and Token Management --

func (s *Service) Authenticate(ctx context.Context, gotToken string) (uuid.UUID, uuid.UUID, error) {
	if !strings.HasPrefix(gotToken, string(token.SessionPrefix)) {
		return uuid.Nil, uuid.Nil, ErrInvalidToken
	}

	userID, sessionID, err := s.store.GetUserIDBySessionHash(ctx, token.Hash(gotToken))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	return userID, sessionID, nil
}

func (s *Service) ClaimInvite(ctx context.Context, inviteToken, deviceName string) (string, error) {
	deviceName, err := validate.RequiredString(deviceName, MaxDeviceNameLength, ErrEmptyDeviceName, ErrDeviceNameTooLong, false)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(inviteToken, string(token.InvitePrefix)) {
		return "", ErrInvalidInvite
	}

	sessionToken, sessionHash := token.Generate(token.SessionPrefix)

	_, _, err = s.store.ClaimInvite(ctx, token.Hash(inviteToken), sessionHash, deviceName)
	if errors.Is(err, ErrUserNotFound) {
		return "", ErrInvalidInvite
	}
	if err != nil {
		return "", err
	}

	return sessionToken, nil
}

func (s *Service) ReissueInvite(ctx context.Context, id uuid.UUID) (string, error) {
	token, hash := token.Generate(token.InvitePrefix)
	expiresAt := time.Now().Add(InviteExpiryTimeout)

	if _, err := s.store.SetInvite(ctx, id, hash, expiresAt); err != nil {
		return "", err
	}

	return token, nil
}
