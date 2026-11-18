// Package task is the server-side executable allowlist.
package task

import (
	"encoding/json"
	"errors"
	"time"
)

type Spec struct {
	Executable string
	Args       []string
	Timeout    time.Duration
}

type Type struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func Types() []Type { return []Type{{"system-info", "Linux kernel and host information", 10}} }

func Resolve(name string, params json.RawMessage) (Spec, error) {
	var obj map[string]json.RawMessage
	if name != "system-info" || json.Unmarshal(params, &obj) != nil || obj == nil || len(obj) != 0 {
		return Spec{}, errors.New("expected system-info with an empty parameters object")
	}
	return Spec{Executable: "/usr/bin/uname", Args: []string{"-a"}, Timeout: 10 * time.Second}, nil
}
