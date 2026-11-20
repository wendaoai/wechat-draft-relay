package wechatdraft

import (
	"encoding/json"
	"time"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

type BodyMaterial struct {
	ImageID string `json:"imageId"`
	SHA256  string `json:"sha256"`
	URL     string `json:"url"`
}
type Materials struct {
	CoverMediaID string         `json:"coverMediaId"`
	BodyImages   []BodyMaterial `json:"bodyImages"`
}
type State struct {
	v1.Task
	SchemaVersion   int       `json:"schemaVersion"`
	RequestHash     string    `json:"requestHash"`
	TaskTokenHash   string    `json:"taskTokenHash"`
	AcceptedAt      time.Time `json:"acceptedAt"`
	QueuedAt        time.Time `json:"queuedAt"`
	CancelRequested bool      `json:"cancelRequested"`
	Materials       Materials `json:"materials"`
}

func (s State) MarshalJSON() ([]byte, error) {
	type alias State
	return json.Marshal(struct {
		*alias
		RetryAllowed     *bool `json:"retryAllowed,omitempty"`
		CancelAllowed    *bool `json:"cancelAllowed,omitempty"`
		PollAfterSeconds *int  `json:"pollAfterSeconds,omitempty"`
	}{alias: (*alias)(&s)})
}

type InputFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type Receipt struct {
	SchemaVersion int         `json:"schemaVersion"`
	AppID         string      `json:"appid"`
	UUID          string      `json:"uuid"`
	TaskTokenHash string      `json:"taskTokenHash"`
	RequestHash   string      `json:"requestHash"`
	AcceptedAt    time.Time   `json:"acceptedAt"`
	Files         []InputFile `json:"files"`
}
type Recovery struct {
	FirstIsolatedAt time.Time `json:"firstIsolatedAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

func (s *State) view() v1.Task {
	b, _ := json.Marshal(s.Task)
	var t v1.Task
	_ = json.Unmarshal(b, &t)
	t.RetryAllowed = retryAllowed(s)
	t.CancelAllowed = (s.Status == "queued" || s.Status == "running") && s.Phase != "submitting_draft"
	if s.Status == "queued" {
		t.PollAfterSeconds = 7
	} else if s.Status == "running" {
		t.PollAfterSeconds = 3
	}
	return t
}
func cloneState(s *State) *State {
	b, _ := json.Marshal(s)
	var out State
	_ = json.Unmarshal(b, &out)
	return &out
}
func terminal(s *State) bool { return s.Status != "queued" && s.Status != "running" }
func retryAllowed(s *State) bool {
	if s.Status != "failed" || s.Error == nil {
		return false
	}
	switch s.Error.Code {
	case "SERVER_RESTARTED", "QUEUE_TIMEOUT", "TASK_TIMEOUT", "WECHAT_TOKEN_INVALID", "WECHAT_CREDENTIAL_INVALID", "WECHAT_IP_NOT_ALLOWED", "WECHAT_PERMISSION_DENIED", "WECHAT_UNAVAILABLE", "STORAGE_UNAVAILABLE":
		return true
	}
	return false
}
func setFinished(s *State, status string, fault *v1.Fault, now time.Time) {
	s.Status, s.Error, s.UpdatedAt = status, fault, now
	s.FinishedAt = &now
	expiry := now.Add(24 * time.Hour)
	s.ExpiresAt = &expiry
	if fault != nil {
		s.Message = fault.Message
	}
}

func initialState(p v1.Payload, tokenHash, hash string, now time.Time) *State {
	return &State{Task: v1.Task{AppID: p.AppID, UUID: p.UUID, Status: "queued", Phase: "accepted",
		Attempt: 1, Progress: v1.Progress{Completed: 1, Total: len(p.Images) + 3, Percent: 100 / (len(p.Images) + 3)},
		Message: "文章已保存，等待推送", CreatedAt: now, UpdatedAt: now},
		SchemaVersion: 1, TaskTokenHash: tokenHash, RequestHash: hash, AcceptedAt: now, QueuedAt: now}
}
