package user

import (
	"encoding/binary"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	gowebauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type WebAuthnCredential struct {
	ID              uint           `gorm:"primaryKey"`
	CreatedAt       time.Time      `gorm:"autoCreateTime"`
	UpdatedAt       time.Time      `gorm:"autoUpdateTime"`
	DeletedAt       gorm.DeletedAt `gorm:"index"`
	UserID          uint           `gorm:"not null;index:idx_webauthn_credentials_user"`
	CredentialID    []byte         `gorm:"not null;uniqueIndex:idx_webauthn_credentials_cred_id"`
	PublicKey       []byte         `gorm:"not null"`
	AttestationType string         `gorm:"default:''"`
	Transport       pq.StringArray `gorm:"type:text[];default:'{}'"`
	AAGUID          []byte         `gorm:"default:'\\x00000000000000000000000000000000'"`
	SignCount       uint32         `gorm:"default:0"`
	BackupEligible  bool           `gorm:"default:false"`
	BackupState     bool           `gorm:"default:false"`
	Name            string         `gorm:"default:''"`
}

func (WebAuthnCredential) TableName() string {
	return "webauthn_credentials"
}

func toLibraryCredential(wc *WebAuthnCredential) gowebauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, len(wc.Transport))
	for i, t := range wc.Transport {
		transports[i] = protocol.AuthenticatorTransport(t)
	}
	return gowebauthn.Credential{
		ID:              wc.CredentialID,
		PublicKey:       wc.PublicKey,
		AttestationType: wc.AttestationType,
		Transport:       transports,
		Flags: gowebauthn.CredentialFlags{
			BackupEligible: wc.BackupEligible,
			BackupState:    wc.BackupState,
		},
		Authenticator: gowebauthn.Authenticator{
			AAGUID:    wc.AAGUID,
			SignCount: wc.SignCount,
		},
	}
}

type WebAuthnUser struct {
	user        *User
	credentials []gowebauthn.Credential
}

func NewWebAuthnUser(user *User, creds []gowebauthn.Credential) *WebAuthnUser {
	return &WebAuthnUser{user: user, credentials: creds}
}

func (u *WebAuthnUser) WebAuthnID() []byte {
	id := make([]byte, 8)
	binary.BigEndian.PutUint64(id, uint64(u.user.ID))
	return id
}

func (u *WebAuthnUser) WebAuthnName() string {
	return u.user.Email
}

func (u *WebAuthnUser) WebAuthnDisplayName() string {
	return u.user.Name
}

func (u *WebAuthnUser) WebAuthnIcon() string {
	return ""
}

func (u *WebAuthnUser) WebAuthnCredentials() []gowebauthn.Credential {
	return u.credentials
}
