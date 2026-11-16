package config

import (
	"strings"
	"testing"
)

func TestWriteQuorumValues(t *testing.T) {
	for _, c := range []struct {
		setting  string
		declared int
		want     int
		errHas   string
	}{
		// auto: a majority from three servers up, every server below that
		{"auto", 1, 1, ""},
		{"auto", 2, 2, ""},
		{"auto", 3, 2, ""},
		{"auto", 4, 3, ""},
		{"auto", 5, 3, ""},
		{"", 3, 2, ""},
		{"AUTO", 5, 3, ""},
		{"auto", 0, 0, ""}, // no declared count: the caller keeps asking every server it can see
		// numbers
		{"3", 3, 3, ""},
		{"2", 3, 2, ""},
		{"2", 5, 0, "below a majority (3) of the 5 declared servers"},
		{"3", 5, 3, ""},
		{"1", 3, 0, "below a majority (2) of the 3 declared servers"},
		{"1", 1, 1, ""},
		{"1", 2, 1, ""},
		{"4", 3, 0, "more than the 3 declared servers"},
		{"0", 3, 0, "auto or a whole number of servers, 1 or more"},
		{"-1", 3, 0, "auto or a whole number"},
		{"many", 3, 0, "auto or a whole number"},
		{"2", 0, 0, "needs the number of servers declared"},
	} {
		got, err := WriteQuorum(c.setting, c.declared)
		if c.errHas != "" {
			if err == nil || !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("WriteQuorum(%q, %d) = %d, %v; want an error with %q", c.setting, c.declared, got, err, c.errHas)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("WriteQuorum(%q, %d) = %d, %v; want %d", c.setting, c.declared, got, err, c.want)
		}
	}
}

func TestWriteQuorumSetting(t *testing.T) {
	for _, c := range []struct {
		name, file string
		env        map[string]string
		setting    string
		effective  int
		errHas     string
	}{
		{"default is auto", `{"url":"u","owner":"o","servers":3}`, nil, "auto", 2, ""},
		{"a number in the file", `{"url":"u","owner":"o","servers":3,"write_quorum":3}`, nil, "3", 3, ""},
		{"a string in the file", `{"url":"u","owner":"o","servers":5,"write_quorum":"4"}`, nil, "4", 4, ""},
		{"auto in the file", `{"url":"u","owner":"o","servers":5,"write_quorum":"auto"}`, nil, "auto", 3, ""},
		{"env wins", `{"url":"u","owner":"o","servers":3,"write_quorum":3}`, map[string]string{"MASH_WRITE_QUORUM": "2"}, "2", 2, ""},
		{"a minority is refused when used", `{"url":"u","owner":"o","servers":3,"write_quorum":1}`, nil, "1", 0, "below a majority"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			writeFile(t, home, c.file)
			cfg, err := load(envOf(c.env), home)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.WriteQuorum != c.setting {
				t.Fatalf("setting %q, want %q", cfg.WriteQuorum, c.setting)
			}
			n, err := cfg.EffectiveWriteQuorum()
			if c.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), c.errHas) {
					t.Fatalf("want an error with %q, got %d, %v", c.errHas, n, err)
				}
				return
			}
			if err != nil || n != c.effective {
				t.Fatalf("effective %d, %v; want %d", n, err, c.effective)
			}
		})
	}
}

func TestWriteQuorumFileValueMustBeAStringOrNumber(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"u","owner":"o","write_quorum":[2]}`)
	if _, err := load(envOf(nil), home); err == nil || !strings.Contains(err.Error(), `"write_quorum"`) {
		t.Fatalf("an array was accepted: %v", err)
	}
}
