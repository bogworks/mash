package sapsdk

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/bogworks/synadia-agents.go/identity"
	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/sap"
)

// SignRefusal is the class of a signing refusal: one of a fixed list of mash's own words, never the SDK's text.
type SignRefusal string

// The signing refusal classes (TO-8). The words are what a command prints after "could not sign: ".
const (
	RefusedAccount     SignRefusal = "your account is not valid"
	RefusedSubject     SignRefusal = "the subject is not valid"
	RefusedName        SignRefusal = "your name is too long"
	RefusedMessageID   SignRefusal = "the message id is not valid"
	RefusedUnspecified SignRefusal = "unspecified"
)

// SignError is a signing refusal. Error never carries SDK text.
type SignError struct{ Class SignRefusal }

func (e *SignError) Error() string {
	if e.Class == RefusedUnspecified || e.Class == "" {
		return "could not sign"
	}
	return "could not sign: " + string(e.Class)
}

// Identity is a party's agent ID and the means to sign as it. The zero value cannot sign.
type Identity struct {
	Account string
	User    string
	signer  identity.Signer
}

// NewIdentity builds an identity from a user seed and its account, with the checks of sap.NewIdentity. The account is
// not checked here, as in sap.Identity: a signing call refuses an invalid one (RefusedAccount).
func NewIdentity(seed []byte, account string) (Identity, error) {
	old, err := sap.NewIdentity(seed, account)
	if err != nil {
		return Identity{}, err
	}
	s, err := identity.SignerFromSeed(seed)
	if err != nil {
		return Identity{}, errors.New("sap: user seed is not usable")
	}
	return Identity{Account: old.Account, User: old.User, signer: s}, nil
}

// IdentityFromCredsAt reads a NATS credentials file as sap.IdentityFromCredsAt does (the same checks and errors).
func IdentityFromCredsAt(path string, creds []byte) (Identity, error) {
	old, err := sap.IdentityFromCredsAt(path, creds)
	if err != nil {
		return Identity{}, err
	}
	kp, err := jwt.ParseDecoratedNKey(creds)
	if err != nil {
		return Identity{}, fmt.Errorf("sap: creds seed: %w", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		return Identity{}, err
	}
	return NewIdentity(seed, old.Account)
}

// ID is the canonical text form, account.user.
func (id Identity) ID() string { return id.Account + "." + id.User }

func (id Identity) agent() (identity.AgentID, error) {
	if id.signer == nil {
		return "", &SignError{Class: RefusedUnspecified}
	}
	aid, err := identity.NewAgentID(id.Account, id.User)
	if err != nil {
		return "", &SignError{Class: RefusedAccount}
	}
	return aid, nil
}

// Sign produces a signed header for publishing payload to subject. An empty nonce gets a fresh one. The refusal is a
// *SignError.
func (id Identity) Sign(subject string, payload []byte, name string, now time.Time, nonce string) (sap.SenderHeader, error) {
	aid, err := id.agent()
	if err != nil {
		return sap.SenderHeader{}, err
	}
	h, err := identity.SignHeader(identity.SignOptions{Signer: id.signer, ID: aid, Name: name, Subject: subject,
		Payload: payload, Now: func() time.Time { return now }, Nonce: nonce})
	if err != nil {
		return sap.SenderHeader{}, &SignError{Class: signRefusal(err, name)}
	}
	return fromSDK(h), nil
}

// IDSig signs the registration identity (§13.10).
func (id Identity) IDSig(agent, owner, promptSubject string) (string, error) {
	aid, err := id.agent()
	if err != nil {
		return "", err
	}
	sig, err := identity.SignAgentID(id.signer, aid, agent, owner, promptSubject)
	if err != nil {
		return "", &SignError{Class: signRefusal(err, "")}
	}
	return sig, nil
}

// The SDK's fixed refusal texts (v0.1.0, identity/sign.go). They are matched, never shown.
const (
	sdkSubjectText = "identity: the subject to sign is not a NATS subject"
	sdkNameText    = "identity: the display name is not valid"
	sdkNonceText   = "identity: the nonce must match"
)

func signRefusal(err error, name string) SignRefusal {
	text := err.Error()
	switch {
	case errors.Is(err, identity.ErrInvalidAgentID):
		return RefusedAccount
	case strings.HasPrefix(text, sdkSubjectText):
		return RefusedSubject
	case strings.HasPrefix(text, sdkNameText) && len(utf16.Encode([]rune(name))) > identity.MaxNameLength:
		return RefusedName
	case strings.HasPrefix(text, sdkNonceText):
		return RefusedMessageID
	}
	return RefusedUnspecified
}

// VerifyIDSig checks an id_sig (§13.10).
func VerifyIDSig(user, account, agent, owner, promptSubject, sig string) bool {
	return identity.VerifyAgentID(map[string]string{
		identity.MetadataKeyUserNKey: user, identity.MetadataKeyAccount: account,
		"agent": agent, "owner": owner, identity.MetadataKeyIDSig: sig,
	}, promptSubject)
}

func fromSDK(h identity.SenderHeader) sap.SenderHeader {
	return sap.SenderHeader{V: h.V, Account: h.Account, User: h.User, Name: h.Name, Sub: h.Sub, TS: h.TS, Nonce: h.Nonce, Sig: h.Sig}
}
