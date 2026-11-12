package store

import (
	"strings"
	"testing"
)

func TestZonedRecordError(t *testing.T) {
	plain := Record{Name: "a", Owner: "o"}
	zoned := Record{Name: "b", Owner: "o", Zone: "red"}
	if err := ZonedRecordError(plain); err != nil {
		t.Fatalf("plain: %v", err)
	}
	if err := FirstZonedRecord([]Record{plain, plain}); err != nil {
		t.Fatalf("none zoned: %v", err)
	}
	want := "record b carries zone \"red\": zones are removed (ADR 0091); run `mashctl unzone b` to make it five-token, then upgrade"
	if err := FirstZonedRecord([]Record{plain, zoned}); err == nil || err.Error() != want {
		t.Fatalf("got %v, want %s", err, want)
	}
	if err := ZonedRecordError(zoned); err == nil || !strings.Contains(err.Error(), "mashctl unzone") {
		t.Fatalf("%v", err)
	}
}
