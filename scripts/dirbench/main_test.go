package main

import (
	"regexp"
	"testing"
)

func TestCheckLoopback(t *testing.T) {
	for _, c := range []struct {
		urls []string
		ok   bool
	}{
		{[]string{"nats://127.0.0.1:4222"}, true},
		{[]string{"nats://127.5.6.7:4222", "127.0.0.1:4223"}, true},
		{[]string{"nats://localhost:4222"}, true},
		{[]string{"nats://[::1]:4222"}, true},
		{[]string{"nats://127.0.0.1:1", "nats://10.0.0.5:4222"}, false},
		{[]string{"nats://nats.example.com:4222"}, false},
		{[]string{"nats://127.0.0.1.evil.example:4222"}, false},
		{[]string{"nats://0.0.0.0:4222"}, false},
		{[]string{"nats://192.168.1.2:4222"}, false},
		{[]string{"nats://"}, false},
	} {
		if err := checkLoopback(c.urls); (err == nil) != c.ok {
			t.Errorf("%v: err=%v, want ok=%v", c.urls, err, c.ok)
		}
	}
}

func TestNewBucketName(t *testing.T) {
	re := regexp.MustCompile(`^dirbench-[0-9a-f]{8}$`)
	seen := map[string]bool{}
	for range 200 {
		n, err := newBucketName()
		if err != nil || !re.MatchString(n) {
			t.Fatalf("name %q err %v", n, err)
		}
		seen[n] = true
	}
	if len(seen) < 199 {
		t.Fatalf("names not random: %d distinct of 200", len(seen))
	}
}
