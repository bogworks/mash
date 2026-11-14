package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CleanMax is the longest string Clean returns, in runes before the trailing "...".
const CleanMax = 256

// Clean makes a string that came from a server or a KV record safe to print on a terminal. Ordinary printable text,
// non-ASCII letters included, is unchanged; every control character (C0, C1, DEL) and every invalid UTF-8 byte is
// replaced by a visible escape (\xNN or \uNNNN), never the raw byte; and the result is capped at CleanMax runes
// with a trailing "...".
func Clean(s string) string { return clean(s, CleanMax) }

// Escape is Clean without the length cap, for a finished message that may be long: control characters and invalid
// UTF-8 are escaped, nothing is cut.
func Escape(s string) string { return clean(s, 0) }

// EscapeText is Escape for text whose layout matters (a mail body, a live prompt): "\n" and "\t" pass through, every
// other control character (C0, DEL and C1) and every invalid UTF-8 byte becomes a visible \xNN escape. It is the one
// rule the mail renderer and the live-prompt notice share.
func EscapeText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r <= 0x9f)):
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func clean(s string, max int) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(s); {
		if max > 0 && n >= max {
			b.WriteString("...")
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x80 && unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
		n++
	}
	return b.String()
}

// RedactURL hides the userinfo of a server URL: "nats://user:pass@host" becomes "nats://***@host". A comma-separated
// list is redacted entry by entry. An entry without "://" is read the way the NATS client reads it (it prepends
// nats://), so "user:pass@host:4222" becomes "***@host:4222". An entry with no userinfo is returned unchanged. It is
// safe on input that is not a valid URL: anything before the last "@" in the authority (up to the next "/", "?" or
// "#") is replaced.
func RedactURL(s string) string {
	if strings.Contains(s, ",") {
		parts := strings.Split(s, ",")
		for i, p := range parts {
			parts[i] = redactOne(p)
		}
		return strings.Join(parts, ",")
	}
	return redactOne(s)
}

func redactOne(s string) string {
	prefix := ""
	rest := s
	if i := strings.Index(s, "://"); i >= 0 {
		prefix, rest = s[:i+3], s[i+3:]
	} else {
		trimmed := strings.TrimLeft(s, " \t")
		prefix, rest = s[:len(s)-len(trimmed)], trimmed
	}
	end := len(rest)
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		end = j
	}
	at := strings.LastIndex(rest[:end], "@")
	if at < 0 {
		return s
	}
	return prefix + "***@" + rest[at+1:]
}

// RedactErr returns err with every occurrence of the server URL u (or, for a list, of any one of its entries that
// carries credentials) in its text replaced by the redacted form. An error whose text does not carry the URL, or a
// URL without credentials, comes back as it was.
func RedactErr(err error, u string) error {
	if err == nil || RedactURL(u) == u {
		return err
	}
	msg := err.Error()
	out := strings.ReplaceAll(msg, u, RedactURL(u))
	for _, p := range strings.Split(u, ",") {
		if r := redactOne(p); r != p && strings.TrimSpace(p) != "" {
			out = strings.ReplaceAll(out, strings.TrimSpace(p), strings.TrimSpace(r))
		}
	}
	if out == msg {
		return err
	}
	return errors.New(out)
}

// HasUserinfo reports whether a server URL carries credentials ("user@" or "user:pass@" before the host).
func HasUserinfo(s string) bool {
	return RedactURL(s) != s
}
