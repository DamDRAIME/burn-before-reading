package users

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	// -- Accounts ----

	Create(ctx context.Context, params CreateParams) (*User, error)

	Get(ctx context.Context, userID uuid.UUID) (*User, error)

	List(ctx context.Context, limit, offset int) ([]*User, error)

	// -- Printer Links ----

	ListMembers(ctx context.Context, printerID uuid.UUID) ([]*User, error)

	// Revokes the membership (not the owner) to a printer
	RevokeMembership(ctx context.Context, userID, printerID uuid.UUID) error

	// Revokes the caller's own membership to a printer (owners get ErrOwnerRoleImmutable)
	SelfRevokeMembership(ctx context.Context, userID, printerID uuid.UUID) error

	SetPrinters(ctx context.Context, userID uuid.UUID, printerAccesses []PrinterAccess) (*User, error)

	// Assigns a user the role of Owner on a given printer
	SetOwnership(ctx context.Context, userID, printerID uuid.UUID) (*User, error)

	// Demotes the owner of given printer to Member, if keepAsMember, else remove the link completely
	DemoteOwner(ctx context.Context, printerID uuid.UUID, keepAsMember bool) error

	CanSend(ctx context.Context, userID, printerID uuid.UUID) (bool, error)

	IsOwner(ctx context.Context, userID, printerID uuid.UUID) (bool, error)

	// -- Sessions ----

	GetUserIDBySessionHash(ctx context.Context, hash []byte) (uuid.UUID, uuid.UUID, error)

	ListSessions(ctx context.Context, userID uuid.UUID) ([]*Session, error)

	RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error

	RevokeAllSessions(ctx context.Context, userID uuid.UUID) error

	// -- Invite Tokens ----

	ClaimInvite(ctx context.Context, inviteHash, sessionHash []byte, deviceName string) (uuid.UUID, uuid.UUID, error)

	// Replaces the user's pending invite, and revokes all their sessions when revokeAllSession is true
	SetInvite(ctx context.Context, userID uuid.UUID, inviteHash []byte, inviteExpiresAt time.Time, revokeAllSessions bool) error

	// -- Join Tokens ----

	CreateJoinToken(ctx context.Context, params CreateJoinTokenParams) (*JoinToken, error)

	ListJoinTokens(ctx context.Context, printerID uuid.UUID, limit, offset int) ([]*JoinToken, error)

	RevokeJoinToken(ctx context.Context, printerID, tokenID uuid.UUID) error

	// Creates a new account that is a member of the token's printer, and its first session
	SignupWithJoinToken(ctx context.Context, params SignupParams) (uuid.UUID, uuid.UUID, error)

	// Adds an existing account as a member fo the token's printer, and return that printer's ID
	RedeemJoinToken(ctx context.Context, userID uuid.UUID, joinTokenHash []byte) (uuid.UUID, error)
}

type CreateParams struct {
	UserName        string
	PrinterAccesses []PrinterAccess
	InviteTokenHash []byte
	TokenExpiresAt  time.Time
}

type CreateJoinTokenParams struct {
	PrinterID      uuid.UUID
	CreatedBy      *uuid.UUID
	JoinTokenHash  []byte
	TokenExpiresAt time.Time
}

type SignupParams struct {
	UserName         string
	DeviceName       string
	JoinTokenHash    []byte
	SessionTokenHash []byte
}
