package auth

import (
	"strings"
	"testing"
)

func TestBearerValidation(t *testing.T) {
	token := strings.Repeat("ab", 32)
	v, err := New(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"Bearer " + token, "bearer " + token, "BEARER   " + token} {
		if !v.Allows([]string{header}) {
			t.Error("valid bearer rejected")
		}
	}
	for _, headers := range [][]string{
		nil, {""}, {"Basic " + token}, {"Bearer"}, {"Bearer "},
		{"Bearer " + strings.Repeat("cd", 32)}, {"Bearer " + token + " "},
		{"Bearer\t" + token}, {"Bearer " + token + ", Bearer " + token},
		{"Bearer " + token, "Bearer " + token}, {"Bearer " + strings.ToUpper(token)},
	} {
		if v.Allows(headers) {
			t.Error("invalid credentials accepted")
		}
	}
	if (Verifier{}).Allows([]string{"Bearer " + token}) {
		t.Fatal("zero value allowed access")
	}
	for _, bad := range []string{"", strings.Repeat("a", 63), strings.Repeat("z", 64), token + "\n"} {
		if _, err := New(bad); err == nil {
			t.Fatal("invalid configured token accepted")
		}
	}
}
