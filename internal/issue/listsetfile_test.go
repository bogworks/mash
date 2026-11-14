package issue

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/store"
)

// listSetDir writes a good list set file into a fresh 0700 directory and returns the directory and file path.
func listSetDir(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(listset.ListSet{
		Range: "foo",
		Switches: []listset.SwitchEntry{
			{Group: "foo.c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "foo.c", Verb: listset.Status, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Heartbeat, Switch: listset.Closed},
		},
		Senders: []listset.SenderLists{{Sender: "foo.c.core", Allow: []string{"foo.c.lead"}}, {Sender: "foo.c.lead"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "listset.json")
	if err := os.WriteFile(p, raw, 0o644); err != nil { // read by others is fine: the list set is not secret
		t.Fatal(err)
	}
	return dir, p
}

func inForce(f *FileListSet) error {
	_, _, err := f.InForce(context.Background())
	return err
}

func TestFileListSetAcceptsAGoodFile(t *testing.T) {
	_, p := listSetDir(t)
	for _, mode := range []os.FileMode{0o600, 0o644, 0o640} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := inForce(&FileListSet{Path: p}); err != nil {
			t.Fatalf("mode %04o: %v", mode, err)
		}
	}
}

func TestFileListSetRefusesASymlink(t *testing.T) {
	dir, p := listSetDir(t)
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(p, link); err != nil {
		t.Skip(err)
	}
	if err := inForce(&FileListSet{Path: link}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked file: %v", err)
	}
}

func TestFileListSetRefusesAFileOthersCanWrite(t *testing.T) {
	_, p := listSetDir(t)
	for _, mode := range []os.FileMode{0o660, 0o664, 0o666, 0o602} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := inForce(&FileListSet{Path: p}); err == nil || !strings.Contains(err.Error(), "chmod go-w") {
			t.Errorf("mode %04o: %v", mode, err)
		}
	}
}

func TestFileListSetRefusesAForeignOwner(t *testing.T) {
	// A foreign owner cannot be made without root, so the owner check is replaced: this tests that a refusal from it
	// is a refusal of the list set (ctlkeys.CheckOwned itself is tested in internal/ctlkeys).
	_, p := listSetDir(t)
	f := &FileListSet{Path: p, owned: func(fs.FileInfo, string, string) error { return errors.New("owned by another uid") }}
	if err := inForce(f); err == nil || !strings.Contains(err.Error(), "another uid") {
		t.Fatalf("%v", err)
	}
}

func TestFileListSetRefusesANonRegularFile(t *testing.T) {
	dir, _ := listSetDir(t)
	if err := inForce(&FileListSet{Path: dir}); err == nil {
		t.Fatal("a directory was accepted")
	}
}

func TestFileListSetChecksItsDirectory(t *testing.T) {
	for _, mode := range []os.FileMode{0o770, 0o720, 0o777, 0o757} {
		dir, p := listSetDir(t)
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		err := inForce(&FileListSet{Path: p})
		_ = os.Chmod(dir, 0o700)
		if err == nil || !strings.Contains(err.Error(), "chmod go-w "+dir) {
			t.Errorf("directory mode %04o: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o700, 0o755} {
		dir, p := listSetDir(t)
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := inForce(&FileListSet{Path: p}); err != nil {
			t.Errorf("directory mode %04o: %v", mode, err)
		}
	}
	dir, _ := listSetDir(t)
	link := filepath.Join(t.TempDir(), "dirlink")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip(err)
	}
	if err := inForce(&FileListSet{Path: filepath.Join(link, "listset.json")}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked directory: %v", err)
	}
}

// The checks run on every read: a file loosened or swapped for a symlink after start is refused, not served from the
// table compiled earlier, and the service signs nothing.
func TestFileListSetIsCheckedAgainOnEveryRequest(t *testing.T) {
	dir, p := listSetDir(t)
	fx := newFixture(t)
	fx.is.ListSet = &FileListSet{Path: p}
	ctx := context.Background()
	if _, err := fx.is.Issue(ctx, fx.req(t, "foo.c.core", time.Minute), store.PurposeIssue); err != nil {
		t.Fatalf("a good file: %v", err)
	}
	signed := len(fx.rec.recs)

	refused := func(what string) {
		t.Helper()
		fx.logs.Reset()
		_, err := fx.is.Issue(ctx, fx.req(t, "foo.c.core", time.Minute), store.PurposeIssue)
		if code(err) != CodeNoListSet {
			t.Fatalf("%s: %v", what, err)
		}
		if len(fx.rec.recs) != signed {
			t.Fatalf("%s: a credential was signed", what)
		}
		if !strings.Contains(fx.logs.String(), "no list set in force") || !strings.Contains(fx.logs.String(), "reason") {
			t.Fatalf("%s: the reason is not logged: %s", what, fx.logs)
		}
	}

	if err := os.Chmod(p, 0o666); err != nil {
		t.Fatal(err)
	}
	refused("a file loosened after start")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.is.Issue(ctx, fx.req(t, "foo.c.core", time.Minute), store.PurposeIssue); err != nil {
		t.Fatalf("restored: %v", err)
	}
	signed = len(fx.rec.recs)

	other := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(other, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, p); err != nil {
		t.Skip(err)
	}
	refused("a file swapped for a symlink")
	_ = os.Remove(p)
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	refused("a directory loosened after start")
	_ = os.Chmod(dir, 0o700)
}
