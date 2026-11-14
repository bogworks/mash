package ctlkeys

import (
	"errors"

	"github.com/nats-io/nkeys"
)

// HeldKeys is the account's public key and every public key the key store holds (operator, system and account key), each with what it is. A submitter, and a next submitter of a planned rotation, must not be one of them:
// it is a key the outside system holds. Keys the store does not have (a store of a server mash did not stand up has
// no operator or system key) are skipped. Any other failure to read the store is an error: a caller that checks a key
// against the result must refuse then (fail closed).
func (a *Authority) HeldKeys() (account string, held map[string]string, err error) {
	held = map[string]string{}
	add := func(kp nkeys.KeyPair, pub, what string) {
		kp.Wipe()
		held[pub] = what
	}
	if kp, pub, err := a.Operator(); err == nil {
		add(kp, pub, "the operator key")
	} else if !errors.Is(err, ErrNoOperatorKey) {
		return "", nil, err
	}
	if kp, pub, err := a.System(); err == nil {
		add(kp, pub, "the system account key")
	} else if !errors.Is(err, ErrNoSystemKey) {
		return "", nil, err
	}
	kp, account, err := a.Account()
	if err != nil {
		return "", nil, err
	}
	add(kp, account, "the account's own key")
	return account, held, nil
}
