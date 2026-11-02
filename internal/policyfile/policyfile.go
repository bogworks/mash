// Package policyfile edits a policy file in place without disturbing it: comments, blank lines, key order and the
// author's flow or block style all survive, because an edit is a small splice into the file's text at positions the
// YAML node tree reports, not a re-encoding of the tree (which would drop blank lines and reflow comments). Every
// result is checked with policy.Parse before Save writes it, so an invalid policy is never written.
//
// A Doc holds an advisory lock on <path>.lock from Load (or Create) until Close, so two mashctl runs do not
// interleave their edits. The lock fails fast with ErrLocked rather than waiting.
package policyfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/bogworks/mash/internal/policy"
	"gopkg.in/yaml.v3"
)

// ErrLocked is returned when another mashctl holds the policy file's lock.
var ErrLocked = errors.New("policy file is being edited by another mashctl")

// rename is os.Rename; tests replace it to simulate a failing rename.
var rename = os.Rename

// Doc is a policy file open for editing.
type Doc struct {
	path string
	data []byte
	mode fs.FileMode
	lock *os.File
}

// Load takes the file's lock, reads the file and validates it with policy.Parse. The caller must Close the Doc.
func Load(path string) (*Doc, error) {
	lock, err := acquire(path)
	if err != nil {
		return nil, err
	}
	d, err := read(path, lock)
	if err != nil {
		lock.Close()
		return nil, err
	}
	return d, nil
}

func read(path string, lock *os.File) (*Doc, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if _, err := policy.Parse(data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Doc{path: path, data: data, mode: st.Mode().Perm(), lock: lock}, nil
}

const template = `# mash policy file: who may mail whom, as labels and rules.
# Schema and examples: docs/policy-file.md
# Check it with "mashctl policy check", run its tests with "mashctl policy test".

default: %s

endpoints: {}

rules: []

tests: []
`

// Create writes a minimal commented policy file with the given default ("allow" or "deny") and returns it open for
// editing. It refuses to overwrite an existing file. The file is created with mode 0600.
func Create(path string, defaultMode string) (*Doc, error) {
	if defaultMode != policy.Allow && defaultMode != policy.Deny {
		return nil, fmt.Errorf("policy file: default %q is not allow or deny", defaultMode)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := acquire(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		lock.Close()
		return nil, fmt.Errorf("%s already exists", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		lock.Close()
		return nil, err
	}
	d := &Doc{path: path, data: []byte(fmt.Sprintf(template, defaultMode)), mode: 0o600, lock: lock}
	if err := d.Save(); err != nil {
		lock.Close()
		return nil, err
	}
	return d, nil
}

func acquire(path string) (*os.File, error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return f, nil
}

// Close releases the lock. It is safe to call more than once.
func (d *Doc) Close() error {
	if d.lock == nil {
		return nil
	}
	syscall.Flock(int(d.lock.Fd()), syscall.LOCK_UN)
	err := d.lock.Close()
	d.lock = nil
	return err
}

// Path is the file the Doc was loaded from.
func (d *Doc) Path() string { return d.path }

// Bytes is the file as it would be saved.
func (d *Doc) Bytes() ([]byte, error) { return append([]byte(nil), d.data...), nil }

// Save writes the file atomically: a temp file in the same directory, fsync, rename. It keeps the old file's
// permission bits (0600 for a file that did not exist). It refuses, writing nothing, when the result does not pass
// policy.Parse.
func (d *Doc) Save() error {
	if _, err := policy.Parse(d.data); err != nil {
		return fmt.Errorf("refusing to save an invalid policy: %w", err)
	}
	dir := filepath.Dir(d.path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(d.path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(d.mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(d.data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := rename(name, d.path); err != nil {
		os.Remove(name)
		return err
	}
	if df, err := os.Open(dir); err == nil {
		df.Sync()
		df.Close()
	}
	return nil
}

// AddEndpoint declares a new endpoint with the given labels. It is an error when the endpoint exists.
func (d *Doc) AddEndpoint(name string, labels map[string]string) error {
	t, err := d.tree()
	if err != nil {
		return err
	}
	if t.endpoint(name) != nil {
		return fmt.Errorf("endpoint %q already exists in %s", name, d.path)
	}
	return d.apply(t.addEndpoint(name, labels))
}

// SetLabels sets the labels in set and removes the keys in unset (a key not present is ignored). An endpoint with no
// labels: gets one; removing the last label removes the labels: key.
func (d *Doc) SetLabels(name string, set map[string]string, unset []string) error {
	t, err := d.tree()
	if err != nil {
		return err
	}
	if t.endpoint(name) == nil {
		return fmt.Errorf("endpoint %q is not declared in %s", name, d.path)
	}
	return d.apply(t.setLabels(name, set, unset))
}

// RemoveEndpoint deletes the endpoint's entry. It returns, as refs, the tests that still name it as from or to
// ("tests[N] from->to", N counted from 1): those tests are left alone, and Save refuses the file until they are
// fixed. Rules select endpoints by label and never name one, so they are untouched.
func (d *Doc) RemoveEndpoint(name string) (refs []string, err error) {
	t, err := d.tree()
	if err != nil {
		return nil, err
	}
	if t.endpoint(name) == nil {
		return nil, fmt.Errorf("endpoint %q is not declared in %s", name, d.path)
	}
	f, err := policy.Parse(d.data)
	if err != nil {
		return nil, err
	}
	for i, tc := range f.Tests {
		if tc.From == name || tc.To == name {
			refs = append(refs, fmt.Sprintf("tests[%d] %s->%s", i+1, tc.From, tc.To))
		}
	}
	return refs, d.apply(t.removeEndpoint(name))
}

// apply performs the splices an operation planned, checks that the text is still YAML and keeps it.
func (d *Doc) apply(edits []edit, err error) error {
	if err != nil {
		return err
	}
	out := splice(d.data, edits)
	var n yaml.Node
	if err := yaml.Unmarshal(out, &n); err != nil {
		return fmt.Errorf("internal error: the edit produced invalid YAML: %w", err)
	}
	d.data = out
	return nil
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}

func flowLabels(m map[string]string, order []string) string {
	parts := make([]string, 0, len(order))
	for _, k := range order {
		parts = append(parts, k+": "+scalar(m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// scalar renders s as YAML, quoted when plain would read as something else.
func scalar(s string) string {
	b, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimRight(string(b), "\n")
}
