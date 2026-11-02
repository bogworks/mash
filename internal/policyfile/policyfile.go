// Package policyfile edits a policy file in place without disturbing it: comments, blank lines, key order and the
// author's flow or block style all survive, because an edit is a small splice into the file's text at positions the
// YAML node tree reports, not a re-encoding of the tree (which would drop blank lines and reflow comments). Every
// result is checked with policy.Parse before Save writes it, so an invalid policy is never written.
//
// A Doc holds an advisory lock on <path>.lock from Load (or Create) until Close, so two mashctl runs do not
// interleave their edits. The lock fails fast with ErrLocked rather than waiting.
package policyfile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"

	"github.com/bogworks/mash/internal/policy"
	"gopkg.in/yaml.v3"
)

// ErrLocked is returned when another mashctl holds the policy file's lock.
var ErrLocked = errors.New("policy file is being edited by another mashctl")

// ErrChanged is returned by Save when the policy file changed on disk since it was loaded.
var ErrChanged = errors.New("changed since it was loaded; reload and redo the edit")

// beforeCreatePublish is a test seam called between Create's checks and its publish.
var beforeCreatePublish func()

// afterSplice is a test seam: it receives an edit's result before the post-condition checks it.
var afterSplice func([]byte) []byte

// rename is os.Rename; tests replace it to simulate a failing rename.
var rename = os.Rename

// Doc is a policy file open for editing.
type Doc struct {
	path string
	data []byte
	orig []byte // the bytes on disk when loaded or last saved; Save refuses if the file no longer holds them
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
	return &Doc{path: path, data: data, orig: append([]byte(nil), data...), mode: st.Mode().Perm(), lock: lock}, nil
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
		return nil, existsErr(path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		lock.Close()
		return nil, err
	}
	data := []byte(fmt.Sprintf(template, defaultMode))
	if err := publishNew(path, data); err != nil {
		lock.Close()
		return nil, err
	}
	return &Doc{path: path, data: data, orig: append([]byte(nil), data...), mode: 0o600, lock: lock}, nil
}

func existsErr(path string) error {
	return fmt.Errorf("policy file %s exists, not overwritten: %w", path, os.ErrExist)
}

// publishNew puts data at path with mode 0600 only if nothing is there, atomically: the content is written and
// fsynced to a temp file, which is then hard-linked to path (link fails with EEXIST rather than replacing a file that
// appeared after the caller's check).
func publishNew(path string, data []byte) error {
	name, err := writeTemp(path, data, 0o600)
	if err != nil {
		return err
	}
	if beforeCreatePublish != nil {
		beforeCreatePublish()
	}
	err = os.Link(name, path)
	os.Remove(name)
	switch {
	case err == nil:
		syncDir(path)
		return nil
	case errors.Is(err, fs.ErrExist):
		return existsErr(path)
	case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP):
		// The filesystem has no hard links: fall back to an O_EXCL create, which is also no-replace but not
		// atomic in content (a crash can leave a short file).
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				return existsErr(path)
			}
			return err
		}
		if _, err := f.Write(data); err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(path)
			return err
		}
		syncDir(path)
		return nil
	default:
		return err
	}
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

// writeTemp writes data to a fsynced temp file next to path with the given mode and returns its name.
func writeTemp(path string, data []byte, mode fs.FileMode) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func syncDir(path string) {
	if df, err := os.Open(filepath.Dir(path)); err == nil {
		df.Sync()
		df.Close()
	}
}

// Save writes the file atomically: a temp file in the same directory, fsync, rename. It keeps the old file's
// permission bits. It refuses, writing nothing, when the result does not pass policy.Parse, and with ErrChanged when
// the file on disk no longer holds the bytes this Doc loaded (or last saved): a hand edit made while the Doc was open
// is never overwritten. The comparison is of contents, under the lock; the lock is advisory, so an editor that does
// not take it can still slip in between the comparison and the rename, a window of microseconds.
func (d *Doc) Save() error {
	if _, err := policy.Parse(d.data); err != nil {
		return fmt.Errorf("refusing to save an invalid policy: %w", err)
	}
	cur, err := os.ReadFile(d.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("policy file %s: %w", d.path, ErrChanged)
		}
		return err
	}
	if !bytes.Equal(cur, d.orig) {
		return fmt.Errorf("policy file %s: %w", d.path, ErrChanged)
	}
	name, err := writeTemp(d.path, d.data, d.mode)
	if err != nil {
		return err
	}
	if err := rename(name, d.path); err != nil {
		os.Remove(name)
		return err
	}
	syncDir(d.path)
	d.orig = append([]byte(nil), d.data...)
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
	want, err := d.snapshot()
	if err != nil {
		return err
	}
	want.endpoints[name] = copyLabels(labels)
	return d.apply(want)(t.addEndpoint(name, labels))
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
	want, err := d.snapshot()
	if err != nil {
		return err
	}
	l := want.endpoints[name]
	for _, k := range unset {
		delete(l, k)
	}
	for k, v := range set {
		l[k] = v
	}
	return d.apply(want)(t.setLabels(name, set, unset))
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
	want, err := d.snapshot()
	if err != nil {
		return nil, err
	}
	delete(want.endpoints, name)
	for i, tc := range f.Tests {
		if tc.From == name || tc.To == name {
			refs = append(refs, fmt.Sprintf("tests[%d] %s->%s", i+1, tc.From, tc.To))
		}
	}
	if err := d.apply(want)(t.removeEndpoint(name)); err != nil {
		return nil, err
	}
	return refs, nil
}

// snap is what an edit may change: the endpoints and their labels, as the YAML decoder reads them, and everything
// else in the file, which an edit must leave as it is.
type snap struct {
	endpoints map[string]map[string]string
	rest      map[string]any
}

func parseSnap(data []byte) (*snap, error) {
	var doc struct {
		Endpoints map[string]struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"endpoints"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var rest map[string]any
	if err := yaml.Unmarshal(data, &rest); err != nil {
		return nil, err
	}
	delete(rest, "endpoints")
	s := &snap{endpoints: map[string]map[string]string{}, rest: rest}
	for n, e := range doc.Endpoints {
		s.endpoints[n] = copyLabels(e.Labels)
	}
	return s, nil
}

func copyLabels(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func (d *Doc) snapshot() (*snap, error) { return parseSnap(d.data) }

// apply returns the function that performs the splices an operation planned, with want as the file's content
// afterwards. It checks that the text is still YAML and that re-reading it gives exactly want: the same endpoint
// names, each with the same labels, and the rest of the file unchanged. It keeps the text only then, so a splice that
// went wrong in a shape nobody anticipated is an error, never a policy that differs from what was asked.
func (d *Doc) apply(want *snap) func([]edit, error) error {
	return func(edits []edit, err error) error {
		if err != nil {
			return err
		}
		out := splice(d.data, edits)
		if afterSplice != nil {
			out = afterSplice(out)
		}
		got, err := parseSnap(out)
		if err != nil {
			return fmt.Errorf("internal error: the edit produced invalid YAML: %w", err)
		}
		if !reflect.DeepEqual(got.endpoints, want.endpoints) || !reflect.DeepEqual(got.rest, want.rest) {
			return fmt.Errorf("internal error: the edit would not have produced the intended policy (endpoints now %s, wanted %s); nothing changed, edit %s by hand", describe(got.endpoints), describe(want.endpoints), d.path)
		}
		d.data = out
		return nil
	}
}

func describe(m map[string]map[string]string) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, n+flowLabels(m[n], sortedKeys(m[n])))
	}
	return "[" + strings.Join(parts, " ") + "]"
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
