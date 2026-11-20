package cmdcore

import (
	"reflect"
	"strings"
	"testing"
)

// A duplicate json name cannot be written as a literal struct without go vet objecting, so the type is built here.
func TestDuplicateJSONNamePanics(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeOf(""), Tag: `json:"same" flag:"--a"`},
		{Name: "B", Type: reflect.TypeOf(""), Tag: `json:"same" flag:"--b"`},
	})
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, ".B ") || !strings.Contains(msg, "json same") {
			t.Errorf("message %q", msg)
		}
	}()
	paramFields(typ)
}
