package endpoint

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
)

// ConnectionFile is the record, in the endpoint's state directory, of the last terminal connection failure its
// session saw. The CLI makes no connection, so `mash status` reads this to say why a session is down. It holds no secret.
const ConnectionFile = "connection.json"

// Connection is the content of ConnectionFile. State is "failed" or "ok"; Error is already redacted by the writer.
type Connection struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	At    string `json:"at"`
}

func connectionPath(cfg config.Config) (string, error) {
	d, err := SpoolDir(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, ConnectionFile), nil
}

// RecordConnection writes the record atomically (temp file and rename, mode 0600). It is best effort: the caller
// ignores the error. errText must already be redacted.
func RecordConnection(cfg config.Config, state, errText string, at time.Time) error {
	p, err := connectionPath(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(Connection{State: state, Error: errText, At: at.UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".connection-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp, 0o600)); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ReadConnection is the recorded connection state of the endpoint cfg configures; ok is false when there is none
// or it cannot be read.
func ReadConnection(cfg config.Config) (c Connection, ok bool) {
	p, err := connectionPath(cfg)
	if err != nil {
		return c, false
	}
	b, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(b, &c) != nil || c.State == "" {
		return Connection{}, false
	}
	return c, true
}

// SetConnectionHook sets f to be called with an authorization failure of this connection (the credentials were
// revoked or reissued), and with nil each time it connects again.
func (e *Endpoint) SetConnectionHook(f func(error)) {
	if e.connHook != nil {
		e.connHook.Store(&f)
	}
}

// IsAuthFailure reports whether err is the server refusing, expiring or revoking the endpoint's credentials.
func IsAuthFailure(err error) bool { return isAuthFailure(err) }

func isAuthFailure(err error) bool {
	if errors.Is(err, nats.ErrAuthorization) || errors.Is(err, nats.ErrAuthExpired) || errors.Is(err, nats.ErrAuthRevoked) ||
		errors.Is(err, nats.ErrAccountAuthExpired) {
		return true
	}
	t := strings.ToLower(err.Error())
	return strings.Contains(t, "authorization violation") || strings.Contains(t, "authentication revoked") ||
		strings.Contains(t, "authentication expired")
}
