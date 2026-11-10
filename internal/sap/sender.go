package sap

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/nats-io/nuid"
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

func signedInput(account, user, subject, ts, nonce string, payload []byte) []byte {
	sum := sha256.Sum256(payload)
	return []byte("AGENT-SENDER-V1\n" + account + "\n" + user + "\n" + subject + "\n" +
		ts + "\n" + nonce + "\n" + hex.EncodeToString(sum[:]) + "\n")
}

// Sign produces a signed header for publishing payload to subject (§13.6).
// An empty nonce gets a fresh NUID.
func (id Identity) Sign(subject string, payload []byte, name string, now time.Time, nonce string) (SenderHeader, error) {
	if id.kp == nil {
		return SenderHeader{}, errors.New("sap: identity has no seed")
	}
	if nonce == "" {
		nonce = nuid.Next()
	}
	ts := now.UTC().Format("2006-01-02T15:04:05Z")
	sig, err := id.kp.Sign(signedInput(id.Account, id.User, subject, ts, nonce, payload))
	if err != nil {
		return SenderHeader{}, err
	}
	return SenderHeader{V: 1, Account: id.Account, User: id.User, Name: name, Sub: subject,
		TS: ts, Nonce: nonce, Sig: base64.RawURLEncoding.EncodeToString(sig)}, nil
}

// IDSig signs the §13.10 registration identity.
func (id Identity) IDSig(agent, owner, promptSubject string) (string, error) {
	if id.kp == nil {
		return "", errors.New("sap: identity has no seed")
	}
	sig, err := id.kp.Sign(idInput(id.User, id.Account, agent, owner, promptSubject))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sig), nil
}

func idInput(user, account, agent, owner, promptSubject string) []byte {
	return []byte("AGENT-ID-V1\n" + user + "\n" + account + "\n" + agent + "\n" + owner + "\n" + promptSubject + "\n")
}

// VerifyIDSig checks a §13.10 id_sig.
func VerifyIDSig(user, account, agent, owner, promptSubject, sig string) bool {
	return verifySig(user, idInput(user, account, agent, owner, promptSubject), sig)
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

var (
	nonceRE   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	accNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func validAccount(a string) bool {
	if strings.HasPrefix(a, "A") && len(a) == 56 && nkeys.IsValidPublicAccountKey(a) {
		return true
	}
	if a == "$G" {
		return true
	}
	// The name form also matches the NKEY shape; an NKEY-shaped name that
	// fails the CRC is not a valid account (§13.3).
	if strings.HasPrefix(a, "A") && len(a) == 56 {
		return false
	}
	return accNameRE.MatchString(a)
}

// parse classifies the header's form (§13.5.1, §13.7 steps 1-2, 4).
// It returns TrustVerified for a well-formed signed header (still to be checked).
func parse(hdr string) (SenderHeader, Trust) {
	if hdr == "" {
		return SenderHeader{}, TrustAbsent
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(hdr), &raw); err != nil {
		return SenderHeader{}, TrustMalformed
	}
	vRaw, ok := raw["v"]
	if !ok {
		return SenderHeader{}, TrustMalformed
	}
	dec := json.NewDecoder(bytes.NewReader(vRaw))
	dec.UseNumber()
	var vAny any
	if err := dec.Decode(&vAny); err != nil {
		return SenderHeader{}, TrustMalformed
	}
	v, isNum := vAny.(json.Number)
	if !isNum {
		return SenderHeader{}, TrustMalformed
	}
	if v.String() != "1" {
		return SenderHeader{}, TrustAbsent
	}
	str := func(k string) (string, bool, bool) { // value, present, ok-shape
		r, present := raw[k]
		if !present {
			return "", false, true
		}
		var s string
		if err := json.Unmarshal(r, &s); err != nil {
			return "", true, false
		}
		return s, true, true
	}
	var h SenderHeader
	h.V = 1
	fields := []struct {
		key string
		dst *string
	}{{"account", &h.Account}, {"user", &h.User}, {"name", &h.Name}, {"sub", &h.Sub},
		{"ts", &h.TS}, {"nonce", &h.Nonce}, {"sig", &h.Sig}}
	present := map[string]bool{}
	for _, f := range fields {
		s, p, ok := str(f.key)
		if !ok {
			return SenderHeader{}, TrustMalformed
		}
		*f.dst, present[f.key] = s, p
	}
	if !present["account"] || !present["user"] || !validAccount(h.Account) || !nkeys.IsValidPublicUserKey(h.User) {
		return SenderHeader{}, TrustMalformed
	}
	if !present["sig"] {
		return h, TrustClaimed
	}
	if !present["sub"] || !present["ts"] || !present["nonce"] || h.Sub == "" || !nonceRE.MatchString(h.Nonce) {
		return SenderHeader{}, TrustMalformed
	}
	if _, err := time.Parse(time.RFC3339Nano, h.TS); err != nil {
		return SenderHeader{}, TrustMalformed
	}
	return h, TrustVerified
}

// VerifyStored classifies a stored message (§13.8): signature and sub only.
func VerifyStored(hdr, arrivalSubject string, payload []byte) (SenderHeader, Trust) {
	h, tr := parse(hdr)
	if tr != TrustVerified {
		return h, tr
	}
	if h.Sub != arrivalSubject ||
		!verifySig(h.User, signedInput(h.Account, h.User, h.Sub, h.TS, h.Nonce, payload), h.Sig) {
		return h, TrustForged
	}
	return h, TrustVerified
}

// LiveSkew is the §13.7 replay window.
const LiveSkew = 30 * time.Second

// VerifyLive classifies a live request (§13.7): signature, sub, ts window, nonce.
func VerifyLive(hdr, arrivalSubject string, payload []byte, now time.Time, seen NonceSet) (SenderHeader, Trust) {
	h, tr := VerifyStored(hdr, arrivalSubject, payload)
	if tr != TrustVerified {
		return h, tr
	}
	ts, _ := time.Parse(time.RFC3339Nano, h.TS)
	if d := now.Sub(ts); d > LiveSkew || d < -LiveSkew {
		return h, TrustForged
	}
	if seen.Seen(h.User, h.Nonce, now) {
		return h, TrustForged
	}
	return h, TrustVerified
}

// NonceSet remembers (user, nonce) pairs for a window.
type NonceSet interface {
	// Seen records the pair and reports whether it was already present.
	Seen(user, nonce string, now time.Time) bool
}

type nonceSet struct {
	mu     sync.Mutex
	window time.Duration
	at     map[string]time.Time
}

// NewNonceSet returns an in-memory NonceSet that forgets pairs after window.
func NewNonceSet(window time.Duration) NonceSet {
	return &nonceSet{window: window, at: map[string]time.Time{}}
}

func (s *nonceSet) Seen(user, nonce string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, t := range s.at {
		if now.Sub(t) > s.window {
			delete(s.at, k)
		}
	}
	k := user + "\x00" + nonce
	if _, ok := s.at[k]; ok {
		return true
	}
	s.at[k] = now
	return false
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
