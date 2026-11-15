package sap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/synadia-agents.go/identity"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// HeaderName is the SAP §13.5 sender header.
const HeaderName = "Agent-Sender"

// Trust is a §13.1 trust class, plus mash's own refinements.
type Trust string

const (
	TrustVerified  Trust = "verified"  // valid signature
	TrustClaimed   Trust = "claimed"   // header without sig
	TrustAbsent    Trust = "absent"    // no header, or unknown v
	TrustForged    Trust = "forged"    // signature present and failing (or replay/skew, live)
	TrustMalformed Trust = "malformed" // §13.5.1 malformed header
	// TrustUnknownSigner is mash's: a valid signature by a user the directory
	// does not know (or knows under another name).
	TrustUnknownSigner Trust = "unknown-signer"
	// TrustStale is mash's: a valid signature made long before the message was
	// stored (or in the future), which is what a replayed capture looks like.
	TrustStale Trust = "stale"
)

// Identity is a party's agent ID (§13.2) and, when it can sign, its seed.
type Identity struct {
	Account string
	User    string
	kp      nkeys.KeyPair
}

// NewIdentity builds an identity from a user seed and its account.
func NewIdentity(seed []byte, account string) (Identity, error) {
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return Identity{}, fmt.Errorf("sap: user seed: %w", err)
	}
	user, err := kp.PublicKey()
	if err != nil {
		return Identity{}, err
	}
	if !nkeys.IsValidPublicUserKey(user) {
		return Identity{}, errors.New("sap: seed is not a user seed")
	}
	return Identity{Account: account, User: user, kp: kp}, nil
}

// IdentityFromCreds reads a NATS credentials file (§13.4: the JWT, not the server).
func IdentityFromCreds(creds []byte) (Identity, error) { return IdentityFromCredsAt("", creds) }

// IdentityFromCredsAt is IdentityFromCreds for the file at path, which an error names (the content is never printed).
func IdentityFromCredsAt(path string, creds []byte) (Identity, error) {
	if err := natsauth.CheckCreds(path, creds); err != nil {
		return Identity{}, err
	}
	tok, err := jwt.ParseDecoratedJWT(creds)
	if err != nil {
		return Identity{}, fmt.Errorf("sap: creds JWT: %w", err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return Identity{}, fmt.Errorf("sap: creds JWT: %w", err)
	}
	kp, err := jwt.ParseDecoratedNKey(creds)
	if err != nil {
		return Identity{}, fmt.Errorf("sap: creds seed: %w", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		return Identity{}, err
	}
	account := uc.Issuer
	if uc.IssuerAccount != "" {
		account = uc.IssuerAccount
	}
	id, err := NewIdentity(seed, account)
	if err != nil {
		return Identity{}, err
	}
	if id.User != uc.Subject {
		return Identity{}, errors.New("sap: creds seed does not match the JWT's user")
	}
	return id, nil
}

// ID is the §13.3 canonical text form.
func (id Identity) ID() string { return id.Account + "." + id.User }

// SenderHeader is the §13.5 Agent-Sender value. Field order is the wire order.
type SenderHeader struct {
	V       int    `json:"v"`
	Account string `json:"account"`
	User    string `json:"user"`
	Name    string `json:"name,omitempty"`
	Sub     string `json:"sub,omitempty"`
	TS      string `json:"ts,omitempty"`
	Nonce   string `json:"nonce,omitempty"`
	Sig     string `json:"sig,omitempty"`
}

// Encode returns compact one-line JSON.
func (h SenderHeader) Encode() string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(h)
	return strings.TrimSuffix(b.String(), "\n")
}

// kpSigner adapts the identity's key pair to the SDK's Signer, so the seed is never copied out of it.
type kpSigner struct {
	pub string
	kp  nkeys.KeyPair
}

func (s kpSigner) PublicKey() string                { return s.pub }
func (s kpSigner) Sign(data []byte) ([]byte, error) { return s.kp.Sign(data) }

// sdkIdentity returns the SDK signer and agent ID for the identity.
func (id Identity) sdkIdentity() (identity.Signer, identity.AgentID, error) {
	if id.kp == nil {
		return nil, "", errors.New("sap: identity has no seed")
	}
	aid, err := identity.NewAgentID(id.Account, id.User)
	if err != nil {
		return nil, "", fmt.Errorf("sap: %w", err)
	}
	return kpSigner{pub: id.User, kp: id.kp}, aid, nil
}

// Sign produces a signed header for publishing payload to subject (§13.6), built by the SAP Go SDK.
// An empty nonce gets a fresh one from the SDK.
func (id Identity) Sign(subject string, payload []byte, name string, now time.Time, nonce string) (SenderHeader, error) {
	signer, aid, err := id.sdkIdentity()
	if err != nil {
		return SenderHeader{}, err
	}
	h, err := identity.SignHeader(identity.SignOptions{Signer: signer, ID: aid, Name: name, Subject: subject,
		Payload: payload, Now: func() time.Time { return now }, Nonce: nonce})
	if err != nil {
		return SenderHeader{}, err
	}
	return fromSDK(h), nil
}

func fromSDK(h identity.SenderHeader) SenderHeader {
	return SenderHeader{V: h.V, Account: h.Account, User: h.User, Name: h.Name, Sub: h.Sub, TS: h.TS, Nonce: h.Nonce, Sig: h.Sig}
}

// IDSig signs the §13.10 registration identity.
func (id Identity) IDSig(agent, owner, promptSubject string) (string, error) {
	signer, aid, err := id.sdkIdentity()
	if err != nil {
		return "", err
	}
	return identity.SignAgentID(signer, aid, agent, owner, promptSubject)
}

// VerifyIDSig checks a §13.10 id_sig.
func VerifyIDSig(user, account, agent, owner, promptSubject, sig string) bool {
	return identity.VerifyAgentID(map[string]string{
		identity.MetadataKeyUserNKey: user, identity.MetadataKeyAccount: account,
		"agent": agent, "owner": owner, identity.MetadataKeyIDSig: sig,
	}, promptSubject)
}

func verifySig(user string, input []byte, sig string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	pk, err := nkeys.FromPublicKey(user)
	if err != nil {
		return false
	}
	return pk.Verify(input, raw) == nil
}

var nonceRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// claim reads the header by the SDK's parser (§13.5.1, §13.7 steps 1-2, 4). final is true when the class is settled
// without a signature check: absent, malformed or claimed. Otherwise out is the signed header, still to be verified.
func claim(hdr string) (out SenderHeader, tr Trust, final bool) {
	if hdr == "" {
		return SenderHeader{}, TrustAbsent, true
	}
	h, err := identity.ParseHeader(hdr)
	if err != nil {
		return SenderHeader{}, TrustMalformed, true
	}
	if h == nil {
		return SenderHeader{}, TrustAbsent, true
	}
	if !h.Signed() {
		return fromSDK(*h), TrustClaimed, true
	}
	return fromSDK(*h), TrustVerified, false
}

// failure maps an SDK verification error: 400 is malformed, anything else (401 rejected, a refused sender) is forged.
func failure(claimed SenderHeader, err error) (SenderHeader, Trust) {
	var e *identity.Error
	if errors.As(err, &e) && e.Code == 400 {
		return SenderHeader{}, TrustMalformed
	}
	return claimed, TrustForged
}

// VerifyStored classifies a stored message (§13.8): signature and sub only.
func VerifyStored(hdr, arrivalSubject string, payload []byte) (SenderHeader, Trust) {
	out, tr, final := claim(hdr)
	if final {
		return out, tr
	}
	_, err := identity.VerifyStored(identity.Message{Subject: arrivalSubject, Data: payload,
		Header: nats.Header{HeaderName: {hdr}}}, identity.StoredOptions{})
	if err != nil {
		return failure(out, err)
	}
	return out, TrustVerified
}

// LiveSkew is the §13.7 replay window.
const LiveSkew = 30 * time.Second

// VerifyLive classifies a live request (§13.7): signature, sub, ts window, nonce.
func VerifyLive(hdr, arrivalSubject string, payload []byte, now time.Time, seen NonceSet) (SenderHeader, Trust) {
	out, tr, final := claim(hdr)
	if final {
		return out, tr
	}
	set, own := seen.(*memNonceSet)
	if !own {
		// Any other NonceSet is asked after the SDK's checks pass, as mash always did; the SDK gets a throwaway set.
		set = newMemNonceSet(LiveSkew)
	}
	set.setNow(now)
	_, err := identity.VerifyLive(identity.Message{Subject: arrivalSubject, Data: payload, Header: nats.Header{HeaderName: {hdr}}},
		identity.LiveOptions{ReplayWindow: LiveSkew, Now: func() time.Time { return now }, Nonces: set.set})
	if err != nil {
		return failure(out, err)
	}
	if !own && seen.Seen(out.User, out.Nonce, now) {
		return out, TrustForged
	}
	return out, TrustVerified
}

// NonceSet remembers (user, nonce) pairs for a window.
type NonceSet interface {
	// Seen records the pair and reports whether it was already present.
	Seen(user, nonce string, now time.Time) bool
}

// memNonceSet is NewNonceSet's value: the SDK's NonceSet, run on the clock the caller last passed in, so a caller that
// supplies its own now (as mash's tests do) is judged by it.
type memNonceSet struct {
	set *identity.NonceSet
	now atomic.Int64 // unix nanoseconds
}

func newMemNonceSet(window time.Duration) *memNonceSet {
	if window < LiveSkew {
		window = LiveSkew // the SDK needs the set's window to cover the replay window
	}
	m := &memNonceSet{}
	m.set = identity.NewNonceSet(identity.NonceSetOptions{Window: window,
		Now: func() time.Time { return time.Unix(0, m.now.Load()).UTC() }})
	return m
}

func (m *memNonceSet) setNow(t time.Time) { m.now.Store(t.UnixNano()) }

// NewNonceSet returns an in-memory NonceSet that forgets pairs after window (at least LiveSkew).
func NewNonceSet(window time.Duration) NonceSet { return newMemNonceSet(window) }

// Seen records the pair at now and reports whether it was already present. A full set or an exhausted per-sender quota
// reports true: the SDK's sets fail closed.
func (m *memNonceSet) Seen(user, nonce string, now time.Time) bool {
	m.setNow(now)
	return m.set.Record(user, nonce, now) != identity.Recorded
}

// instanceInput is what a sender signs to bind its instance id to one message:
// the message's own signed fields, so the binding cannot move to another one.
func instanceInput(h SenderHeader, instance string) []byte {
	return []byte("MASH-SENDER-INSTANCE-V1\n" + h.Account + "\n" + h.User + "\n" + h.Sub + "\n" +
		h.TS + "\n" + h.Nonce + "\n" + instance + "\n")
}

// SignInstance binds instance (the sender's own process id) to the message h
// signs. The value is "<instance>.<sig>", for the Mash-Sender-Instance header.
func (id Identity) SignInstance(h SenderHeader, instance string) (string, error) {
	if id.kp == nil {
		return "", errors.New("sap: identity has no seed")
	}
	if !nonceRE.MatchString(instance) {
		return "", errors.New("sap: instance id is not a plain token")
	}
	sig, err := id.kp.Sign(instanceInput(h, instance))
	if err != nil {
		return "", err
	}
	return instance + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func urgentInput(h SenderHeader) []byte {
	return []byte("MASH-URGENT-V1\n" + h.Account + "\n" + h.User + "\n" + h.Sub + "\n" + h.TS + "\n" + h.Nonce + "\n")
}

// SignUrgent marks the message h signs as urgent: the value of the Mash-Urgent header, a signature over the message's
// own header fields under its own domain, so it holds for this message alone.
func (id Identity) SignUrgent(h SenderHeader) (string, error) {
	if id.kp == nil {
		return "", errors.New("sap: identity has no seed")
	}
	sig, err := id.kp.Sign(urgentInput(h))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyUrgent reports whether value marks the message h (an already verified sender header) urgent, signed by h's
// own key.
func VerifyUrgent(value string, h SenderHeader) bool {
	return value != "" && verifySig(h.User, urgentInput(h), value)
}

// VerifyInstance checks an instance binding against the (already verified)
// sender header of the same message. It returns the instance only when the
// header's own key signed it for this message.
func VerifyInstance(value string, h SenderHeader) (string, bool) {
	i := strings.LastIndexByte(value, '.')
	if i <= 0 || i == len(value)-1 {
		return "", false
	}
	instance, sig := value[:i], value[i+1:]
	if !nonceRE.MatchString(instance) || !verifySig(h.User, instanceInput(h, instance), sig) {
		return "", false
	}
	return instance, true
}

// markSeq is a stream sequence in its one wire form: plain decimal, no sign, no leading zero, at least 1.
func markSeq(s string) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != s {
		return 0, false
	}
	return n, true
}

func inReplyToInput(h SenderHeader, seq uint64) []byte {
	return []byte("MASH-IN-REPLY-TO-V1\n" + h.Account + "\n" + h.User + "\n" + h.Sub + "\n" +
		h.TS + "\n" + h.Nonce + "\n" + strconv.FormatUint(seq, 10) + "\n")
}

// SignInReplyTo binds the sequence seq (the message of the sender's own inbox this one answers) to the message h
// signs. The value is "<seq>.<sig>", for the Mash-In-Reply-To header.
func (id Identity) SignInReplyTo(h SenderHeader, seq uint64) (string, error) {
	if id.kp == nil {
		return "", errors.New("sap: identity has no seed")
	}
	if seq == 0 {
		return "", errors.New("sap: in-reply-to sequence is 0")
	}
	sig, err := id.kp.Sign(inReplyToInput(h, seq))
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(seq, 10) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyInReplyTo checks an in-reply-to value against the (already verified) sender header of the same message. It
// returns the sequence only when the header's own key signed it for this message.
func VerifyInReplyTo(h SenderHeader, value string) (uint64, bool) {
	i := strings.IndexByte(value, '.')
	if i <= 0 || i == len(value)-1 {
		return 0, false
	}
	seq, ok := markSeq(value[:i])
	if !ok || !verifySig(h.User, inReplyToInput(h, seq), value[i+1:]) {
		return 0, false
	}
	return seq, true
}

// validOrigin is the endpoint-name syntax (config.ValidName; sap cannot import config).
var validOrigin = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

func validOriginName(s string) bool { return len(s) >= 1 && len(s) <= 48 && validOrigin.MatchString(s) }

// validViaOrigin is the one rule for a via origin, used by SignVia and VerifyVia: a legacy endpoint name (validOriginName,
// exactly as before), or the flat key of a new-form endpoint, agent_owner_name, in its canonical form (FromFlat accepts
// it and EndpointIdentity.Flat gives back the same string, so no token is empty, upper-case, over length or the legacy
// agent token, and no other spelling is accepted). The origin is signed as the string given. For a sender that is a
// new-form endpoint that string is exactly what its sender name (Name in the SenderHeader, set from the endpoint's
// directory key) is: its Flat form. The signed Sub of that sender is its mail subject, agents.mail.<agent>.<owner>.<name>.
func validViaOrigin(s string) bool {
	if validOriginName(s) {
		return true
	}
	id, err := FromFlat(s)
	return err == nil && id.Flat() == s
}

func viaInput(h SenderHeader, origin string, seq uint64) []byte {
	return []byte("MASH-VIA-V1\n" + h.Account + "\n" + h.User + "\n" + h.Sub + "\n" +
		h.TS + "\n" + h.Nonce + "\n" + origin + "\n" + strconv.FormatUint(seq, 10) + "\n")
}

// SignVia declares, under the key of the message h signs, that this message relays or quotes message seq of the
// signer's own inbox, whose sender was origin (a legacy endpoint name or the flat key of a new-form endpoint, see validViaOrigin). The value is "<origin>#<seq>.<sig>", for the
// Mash-Via header.
func (id Identity) SignVia(h SenderHeader, origin string, seq uint64) (string, error) {
	if id.kp == nil {
		return "", errors.New("sap: identity has no seed")
	}
	if !validViaOrigin(origin) {
		return "", errors.New("sap: via origin is not an endpoint name or flat key")
	}
	if seq == 0 {
		return "", errors.New("sap: via sequence is 0")
	}
	sig, err := id.kp.Sign(viaInput(h, origin, seq))
	if err != nil {
		return "", err
	}
	return origin + "#" + strconv.FormatUint(seq, 10) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyVia checks a via value against the (already verified) sender header of the same message. It returns the
// origin and sequence only when the header's own key signed exactly them for this message.
func VerifyVia(h SenderHeader, value string) (string, uint64, bool) {
	hash := strings.IndexByte(value, '#')
	if hash <= 0 {
		return "", 0, false
	}
	rest := value[hash+1:]
	dot := strings.IndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return "", 0, false
	}
	origin := value[:hash]
	seq, ok := markSeq(rest[:dot])
	if !ok || !validViaOrigin(origin) || !verifySig(h.User, viaInput(h, origin, seq), rest[dot+1:]) {
		return "", 0, false
	}
	return origin, seq, true
}
