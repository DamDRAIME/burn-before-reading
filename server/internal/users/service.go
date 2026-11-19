package users

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"bbr/internal/token"
)

var (
	ErrEmptyUserName    = errors.New("user name cannot be empty")
	ErrUserNameTooLong  = errors.New("user name exceeds maximum length")
	ErrNameAlreadyTaken = errors.New("user name already in use on the printer")

	ErrEmptyDeviceName   = errors.New("device name cannot be empty")
	ErrDeviceNameTooLong = errors.New("device name exceeds maximum length")

	ErrInvalidRole = errors.New("invalid role")

	ErrInvalidJoinToken    = errors.New("invalid join token")
	ErrInvalidSessionToken = errors.New("invalid session token")
	ErrInvalidInviteToken  = errors.New("invalid invite token")

	ErrAlreadyMember             = errors.New("user is already linked to this printer as a member")
	ErrOwnerRoleImmutable        = errors.New("the owner cannot revoke his/her ownership of a printer")
	ErrPrinterOwnershipViolation = errors.New("user already owns a printer or the printer is already owned by another user")
	ErrConflictingRoles          = errors.New("conflicting roles for the same printer")

	ErrNoPrinters = errors.New("user doesn't own a printer")

	ErrUserNotFound      = errors.New("user not found")
	ErrMemberNotFound    = errors.New("member not found")
	ErrOwnerNotFound     = errors.New("printer has no owner")
	ErrSessionNotFound   = errors.New("session not found")
	ErrJoinTokenNotFound = errors.New("join token not found or already used")
	ErrPrinterNotFound   = errors.New("printer not found")  // the printer in the URL
	ErrUnknownPrinter    = errors.New("unknown printer ID") // a printer ID in the request body
)

const (
	MaxUserNameLength   = 20
	MaxDeviceNameLength = 50

	RecoveryInviteTokenExpiryTimeout = 2 * 24 * time.Hour // Applies to invites issued by the Admin (new account or recovery)
	SelfInviteTokenExpiryTimeout     = 15 * time.Minute   // Applies to invites issued directly by the user for their own new device
	JoinTokenExpiryTimeout           = 2 * 24 * time.Hour
)

type SubmitParams struct {
	UserName   string
	PrinterIDs []uuid.UUID
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// -- Accounts ----

// Create generates a new account and returns its invite. Admin only
func (s *Service) Create(ctx context.Context, params SubmitParams) (*User, string, error) {
	name, err := validateUserName(params.UserName)
	if err != nil {
		return nil, "", err
	}

	inviteToken, inviteTokenHash := token.Generate(token.InvitePrefix)

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
		UserName:        name,
		PrinterAccesses: dedupedPrinterAccesses,
		InviteTokenHash: inviteTokenHash,
		TokenExpiresAt:  time.Now().Add(RecoveryInviteTokenExpiryTimeout),
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

// -- Printer links ----

func (s *Service) ListMembers(ctx context.Context, printerID uuid.UUID) ([]*User, error) {
	return s.store.ListMembers(ctx, printerID)
}

func (s *Service) RevokeMembership(ctx context.Context, userID, printerID uuid.UUID) error {
	return s.store.RevokeMembership(ctx, userID, printerID)
}

func (s *Service) SelfRevokeMembership(ctx context.Context, callerID, printerID uuid.UUID) error {
	return s.store.SelfRevokeMembership(ctx, callerID, printerID)
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

func (s *Service) SetOwnership(ctx context.Context, userID, printerID uuid.UUID) (*User, error) {
	return s.store.SetOwnership(ctx, userID, printerID)
}

func (s *Service) DemoteOwner(ctx context.Context, printerID uuid.UUID, keepAsMember bool) error {
	return s.store.DemoteOwner(ctx, printerID, keepAsMember)
}

func (s *Service) CanSend(ctx context.Context, userID, printerID uuid.UUID) (bool, error) {
	return s.store.CanSend(ctx, userID, printerID)
}

func (s *Service) IsOwner(ctx context.Context, userID, printerID uuid.UUID) (bool, error) {
	return s.store.IsOwner(ctx, userID, printerID)
}

// -- Sessions ----

func (s *Service) Authenticate(ctx context.Context, sessionToken string) (uuid.UUID, uuid.UUID, error) {
	if !strings.HasPrefix(sessionToken, string(token.SessionPrefix)) {
		return uuid.Nil, uuid.Nil, ErrInvalidSessionToken
	}

	userID, sessionID, err := s.store.GetUserIDBySessionHash(ctx, token.Hash(sessionToken))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	return userID, sessionID, nil
}

func (s *Service) ListSessions(ctx context.Context, userID uuid.UUID) ([]*Session, error) {
	return s.store.ListSessions(ctx, userID)
}

func (s *Service) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	return s.store.RevokeSession(ctx, userID, sessionID)
}

func (s *Service) RevokeAllSessions(ctx context.Context, userID uuid.UUID) error {
	return s.store.RevokeAllSessions(ctx, userID)
}

// -- Invite Tokens ----

func (s *Service) ClaimInvite(ctx context.Context, inviteToken, deviceName string) (string, error) {
	deviceName, err := validateDeviceName(deviceName)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(inviteToken, string(token.InvitePrefix)) {
		return "", ErrInvalidInviteToken
	}

	sessionToken, sessionHash := token.Generate(token.SessionPrefix)

	_, _, err = s.store.ClaimInvite(ctx, token.Hash(inviteToken), sessionHash, deviceName)
	if errors.Is(err, ErrUserNotFound) {
		return "", ErrInvalidInviteToken
	}
	if err != nil {
		return "", err
	}

	return sessionToken, nil
}

// Reissues a new invite token for account recovery purposes, and, optionally, revoke all sessions
func (s *Service) IssueRecoveryInvite(ctx context.Context, userID uuid.UUID, revokeAllSessions bool) (string, time.Time, error) {
	return s.issueInvite(ctx, userID, RecoveryInviteTokenExpiryTimeout, revokeAllSessions)
}

// Self issued and short-lived invite tokens for the caller's own account to claim on another device.
// callerID must come from the authenticated session, not from the request.
func (s *Service) SelfIssueInvite(ctx context.Context, callerID uuid.UUID) (string, time.Time, error) {
	return s.issueInvite(ctx, callerID, SelfInviteTokenExpiryTimeout, false)
}

func (s *Service) issueInvite(ctx context.Context, userID uuid.UUID, ttl time.Duration, revokeAllSessions bool) (string, time.Time, error) {
	inviteToken, hash := token.Generate(token.InvitePrefix)
	expiresAt := time.Now().Add(ttl)

	if err := s.store.SetInvite(ctx, userID, hash, expiresAt, revokeAllSessions); err != nil {
		return "", time.Time{}, err
	}

	return inviteToken, expiresAt, nil
}

// -- Join Tokens ----

// Generates a join token for the given printer owned by createdBy (or nil of Admin)
func (s *Service) CreateJoinToken(ctx context.Context, printerID uuid.UUID, createdBy *uuid.UUID) (*JoinToken, string, error) {
	joinToken, joinTokenHash := token.Generate(token.JoinPrefix)
	expiresAt := time.Now().Add(JoinTokenExpiryTimeout)

	jt, err := s.store.CreateJoinToken(ctx, CreateJoinTokenParams{
		PrinterID:      printerID,
		CreatedBy:      createdBy,
		JoinTokenHash:  joinTokenHash,
		TokenExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, "", err
	}

	return jt, joinToken, nil
}

func (s *Service) ListJoinTokens(ctx context.Context, printerID uuid.UUID, limit, offset int) ([]*JoinToken, error) {
	return s.store.ListJoinTokens(ctx, printerID, limit, offset)
}

func (s *Service) RevokeJoinToken(ctx context.Context, printerID, tokenID uuid.UUID) error {
	return s.store.RevokeJoinToken(ctx, printerID, tokenID)
}

func (s *Service) SignupWithJoinToken(ctx context.Context, joinToken, userName, deviceName string) (string, error) {
	userName, err := validateUserName(userName)
	if err != nil {
		return "", nil
	}

	deviceName, err = validateDeviceName(deviceName)
	if err != nil {
		return "", nil
	}

	sessionToken, sessionHash := token.Generate(token.SessionPrefix)

	_, _, err = s.store.SignupWithJoinToken(ctx, SignupParams{
		UserName:         userName,
		DeviceName:       deviceName,
		JoinTokenHash:    token.Hash(joinToken),
		SessionTokenHash: sessionHash,
	})
	if err != nil {
		return "", nil
	}

	return sessionToken, nil
}

// Adds the caller's existing account to the token's printer
func (s *Service) RedeemJoinToken(ctx context.Context, callerID uuid.UUID, joinToken string) (*User, error) {
	if !strings.HasPrefix(joinToken, string(token.JoinPrefix)) {
		return nil, ErrInvalidJoinToken
	}

	if _, err := s.store.RedeemJoinToken(ctx, callerID, token.Hash(joinToken)); err != nil {
		return nil, err
	}

	return s.store.Get(ctx, callerID)
}
