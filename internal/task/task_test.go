package task

import "testing"

func TestAllowlist(t *testing.T) {
	for _, tc := range []struct{ name, params string }{{"sh", "{}"}, {"system-info", `{"args":["/etc/passwd"]}`}, {"system-info", "null"}, {"system-info", "[]"}, {"system-info", ""}} {
		if _, err := Resolve(tc.name, []byte(tc.params)); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	s, err := Resolve("system-info", []byte(" {} "))
	if err != nil || s.Executable != "/usr/bin/uname" || len(s.Args) != 1 || s.Args[0] != "-a" {
		t.Fatalf("wrong command %+v %v", s, err)
	}
}
