// Package job defines the persisted job lifecycle.
package job

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	Cancelled State = "cancelled"
	TimedOut  State = "timed_out"
)

func (s State) Terminal() bool {
	return s == Succeeded || s == Failed || s == Cancelled || s == TimedOut
}

func CanTransition(from, to State) bool {
	return (from == Queued && to == Running) || (from == Running && to.Terminal())
}

type Job struct {
	Sequence       int64           `json:"sequence"`
	ID             string          `json:"id"`
	TaskType       string          `json:"task_type"`
	Parameters     json.RawMessage `json:"parameters"`
	State          State           `json:"state"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at"`
	CompletedAt    *time.Time      `json:"completed_at"`
	Attempt        int             `json:"attempt"`
	ExitCode       *int            `json:"exit_code"`
	Error          string          `json:"error"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	LogName        string          `json:"-"`
}

func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error.
	return hex.EncodeToString(b[:])
}

func ValidID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
