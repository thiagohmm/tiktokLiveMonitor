// Package model contains data structures and repository interfaces for the application.
package model

// AnomalyLog represents a single moderation record.
type AnomalyLog struct {
	ID        int64  `json:"id"`
	LiveName  string `json:"live_name"`
	Day       string `json:"day"`
	Timestamp string `json:"timestamp"`
	UniqueID  string `json:"uniqueId"`
	Comment   string `json:"comment"`
	IsAnomaly bool   `json:"is_anomaly"`
	Category  string `json:"category"`
}

// Feedback represents a user-provided classification example.
type Feedback struct {
	Comment  string `json:"comment"`
	Category string `json:"category"`
	Expected string `json:"expected"`
}

// FalsePositive represents a recorded false positive entry.
type FalsePositive struct {
	ID        int    `json:"id"`
	Comment   string `json:"comment"`
	Category  string `json:"category"`
	Expected  string `json:"expected"`
	Timestamp string `json:"timestamp"`
}

// UserMessage represents a user message from a live stream.
type UserMessage struct {
	ID        int64  `json:"id"`
	UniqueID  string `json:"uniqueId"`
	Username  string `json:"username"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// Gift represents a gift received during a live stream.
type Gift struct {
	ID          int64  `json:"id"`
	LiveName    string `json:"live_name"`
	UniqueID    string `json:"uniqueId"`
	Nickname    string `json:"nickname"`
	GiftName    string `json:"giftName"`
	RepeatCount int    `json:"repeatCount"`
	GiftType    int    `json:"giftType"`
	Timestamp   string `json:"timestamp"`
}

// Target gift response types.
const (
	TargetGiftResponseManual    = "manual"
	TargetGiftResponseAutomatic = "automatic"
)

// TargetGiftHistory tracks when a target gift was received and answered.
type TargetGiftHistory struct {
	ID           int64   `json:"id"`
	LiveName     string  `json:"liveName"`
	UniqueID     string  `json:"uniqueId"`
	Nickname     string  `json:"nickname"`
	GiftName     string  `json:"giftName"`
	ReceivedAt   string  `json:"receivedAt"`
	AnsweredAt   *string `json:"answeredAt,omitempty"`
	ResponseType *string `json:"responseType,omitempty"`
}

// PinnedComment is a comment pinned during a live stream.
type PinnedComment struct {
	ID         int64  `json:"id"`
	LiveName   string `json:"liveName"`
	UniqueID   string `json:"uniqueId"`
	Nickname   string `json:"nickname"`
	Comment    string `json:"comment"`
	PinID      string `json:"pinId,omitempty"`
	IsFollower *bool  `json:"isFollower,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// ValidExpected values for feedback.
var ValidExpected = map[string]bool{
	"NAO":              true,
	"SIM_PERGUNTA":     true,
	"SIM_PROSELITISMO": true,
	"SIM_ODIO":         true,
	"SIM_SPAM":         true,
	"SIM_GOLPE":        true,
	"SIM_OUTRO":        true,
}

// ValidCategory values for feedback.
var ValidCategory = map[string]bool{
	"OK":           true,
	"PERGUNTA":     true,
	"PROSELITISMO": true,
	"ODIO":         true,
	"SPAM":         true,
	"GOLPE":        true,
	"OUTRO":        true,
}
