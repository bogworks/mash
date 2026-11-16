package credential

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// KeyUsage and InstallUsage are the usage lines of the two verbs that run on the endpoint's host; prog is "mash" or
// "mashctl".
func KeyUsage(prog string) string { return "usage: " + prog + " credential key --out FILE" }

func InstallUsage(prog string) string {
	return "usage: " + prog + " credential install --seed FILE --jwt FILE --out CREDS [--force]"
}

// maxCLIFile is the largest seed or JWT file the commands read.
const maxCLIFile = 64 * 1024

// ParseFlags reads --name VALUE and --name=VALUE for the names in valued and the bare flags in bools. A value is never
// put in an error. help is true for --help or -h.
func ParseFlags(args []string, valued []string, bools []string) (vals map[string]string, set map[string]bool, help bool, err error) {
	vals, set = map[string]string{}, map[string]bool{}
	for i := 0; i < len(args); i++ {
		name, inline, hasInline := strings.Cut(args[i], "=")
		switch {
		case name == "--help" || name == "-h":
			return nil, nil, true, nil
		case slices.Contains(bools, name) && !hasInline:
			set[name] = true
		case slices.Contains(valued, name):
			if _, dup := vals[name]; dup {
				return nil, nil, false, fmt.Errorf("%s given twice", name)
			}
			switch {
			case hasInline && inline != "":
				vals[name] = inline
			case !hasInline && i+1 < len(args) && args[i+1] != "":
				i++
				vals[name] = args[i]
			default:
				return nil, nil, false, fmt.Errorf("%s needs a value", name)
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return nil, nil, false, fmt.Errorf("unknown flag %s", name)
			}
			return nil, nil, false, errors.New("unexpected argument")
		}
	}
	return vals, set, false, nil
}

// WriteFile0600 writes data to path with mode 0600. Without overwrite the file must not exist (O_EXCL); with it an
// existing file is truncated and its mode set to 0600.
func WriteFile0600(path string, data []byte, overwrite bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s exists; it is not overwritten", path)
		}
		return fmt.Errorf("cannot write %s", path)
	}
	if overwrite {
		if err := f.Chmod(0o600); err != nil {
			f.Close()
			return fmt.Errorf("cannot set the mode of %s", path)
		}
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("cannot write %s", path)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("cannot write %s", path)
	}
	return nil
}

// readSmall reads a regular file of at most maxCLIFile bytes. A file that group or others can read is refused when
// private is true.
func readSmall(path string, private bool) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s", path)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if private && fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users (mode %04o); run chmod 600 on it, and treat the key as exposed if others had access", path, fi.Mode().Perm())
	}
	if fi.Size() > maxCLIFile {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxCLIFile)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s", path)
	}
	return b, nil
}

// KeyCmd is `credential key --out FILE`: it makes a user key, writes the seed to FILE (mode 0600, never over an
// existing file) and prints the public key and the next step. The seed is not printed and the buffer is cleared. The
// return value is the exit code.
func KeyCmd(prog string, args []string, stdout, stderr io.Writer) int {
	vals, _, help, err := ParseFlags(args, []string{"--out"}, nil)
	if help {
		fmt.Fprintln(stdout, KeyUsage(prog))
		return 0
	}
	if err == nil && vals["--out"] == "" {
		err = errors.New("--out is required")
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s credential key: %v\n%s\n", prog, err, KeyUsage(prog))
		return 2
	}
	out := vals["--out"]
	seed, pub, err := NewKey()
	if err != nil {
		fmt.Fprintf(stderr, "%s credential key: %v\n", prog, err)
		return 6
	}
	defer clear(seed)
	body := append(append([]byte{}, seed...), '\n')
	defer clear(body)
	if err := WriteFile0600(out, body, false); err != nil {
		fmt.Fprintf(stderr, "%s credential key: %v\n", prog, err)
		return 6
	}
	fmt.Fprintf(stdout, "public key: %s\n", pub)
	fmt.Fprintf(stdout, "seed written to %s (mode 0600); it never leaves this host\n", out)
	fmt.Fprintf(stdout, "next: on the host with the keys, run\n  mashctl credential issue --address ADDRESS --key %s --out ep.jwt\n", pub)
	return 0
}

// InstallCmd is `credential install --seed FILE --jwt FILE --out CREDS [--force]`: it makes the creds file from the
// endpoint's seed and the JWT mashctl signed for its public key. It refuses a JWT for another key. The return value is
// the exit code.
func InstallCmd(prog string, args []string, stdout, stderr io.Writer) int {
	vals, set, help, err := ParseFlags(args, []string{"--seed", "--jwt", "--out"}, []string{"--force"})
	if help {
		fmt.Fprintln(stdout, InstallUsage(prog))
		return 0
	}
	if err == nil {
		for _, n := range []string{"--seed", "--jwt", "--out"} {
			if vals[n] == "" {
				err = fmt.Errorf("%s is required", n)
				break
			}
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s credential install: %v\n%s\n", prog, err, InstallUsage(prog))
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "%s credential install: %v\n", prog, err)
		return 6
	}
	seedRaw, err := readSmall(vals["--seed"], true)
	if err != nil {
		return fail(err)
	}
	defer clear(seedRaw)
	seed := []byte(strings.TrimSpace(string(seedRaw)))
	defer clear(seed)
	jwtRaw, err := readSmall(vals["--jwt"], false)
	if err != nil {
		return fail(err)
	}
	creds, err := Assemble(strings.TrimSpace(string(jwtRaw)), seed)
	if err != nil {
		return fail(err)
	}
	defer clear(creds)
	if err := WriteFile0600(vals["--out"], creds, set["--force"]); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "creds written to %s (mode 0600)\n", vals["--out"])
	return 0
}
