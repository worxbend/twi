package automation

import "time"

type DropReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type SendResult struct {
	Transport     string      `json:"transport,omitempty"`
	Authorization string      `json:"authorization,omitempty"`
	ForSourceOnly *bool       `json:"for_source_only,omitempty"`
	SchemaVersion int         `json:"schema_version"`
	Type          string      `json:"type"`
	InputIndex    int         `json:"input_index"`
	ClientID      string      `json:"client_id,omitempty"`
	Channel       Channel     `json:"channel"`
	Status        string      `json:"status"`
	MessageID     string      `json:"message_id"`
	ObservedAt    time.Time   `json:"observed_at"`
	DropReason    *DropReason `json:"drop_reason"`
}
