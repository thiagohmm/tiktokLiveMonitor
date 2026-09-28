// Package model defines repository interfaces for data access.
package model

import (
	"errors"
	"time"
)

// ErrCommentRequired is returned when comment is empty.
var ErrCommentRequired = errors.New("comment is required")

// ErrInvalidID is returned when ID is invalid.
var ErrInvalidID = errors.New("invalid id")

// ErrUniqueIDRequired is returned when uniqueId is required but empty.
var ErrUniqueIDRequired = errors.New("uniqueId is required")

// ErrLiveSessionNotFound is returned when a live session id does not exist.
var ErrLiveSessionNotFound = errors.New("live session not found")

// ErrOrgRequired is returned when a tenant-scoped call has no organization.
var ErrOrgRequired = errors.New("organization is required")

// ErrOrgNotFound is returned when an organization or membership does not exist.
var ErrOrgNotFound = errors.New("organization not found")

// ErrOrgInactive is returned when an operation needs an active organization.
var ErrOrgInactive = errors.New("organization inactive")

// ErrLiveNotLegacy is returned when a session to move is not in the legacy
// organization: only legacy sessions change organization.
var ErrLiveNotLegacy = errors.New("live session is not in the legacy organization")

// ErrLiveBeingMonitored is returned when a legacy live is still being monitored
// and so cannot change organization.
var ErrLiveBeingMonitored = errors.New("live is being monitored")

// ErrPixNotFound is returned when a Fila PIX entity does not exist for the organization.
var ErrPixNotFound = errors.New("pix entity not found")

// ErrPixForbidden is returned when a Fila PIX entity belongs to another organization.
var ErrPixForbidden = errors.New("pix entity forbidden")

// ErrPixInvalidTransition is returned when a Fila PIX status transition is invalid.
var ErrPixInvalidTransition = errors.New("pix invalid transition")

// ErrPixMediaGone is returned when the receipt file was already purged.
var ErrPixMediaGone = errors.New("pix media gone")

// Every tenant-scoped read or mutation takes the organization id (orgID). Rows
// of the event tables belong to an organization through their live session
// (live_sessions.org_id), so a session id obtained inside one organization can
// never reach rows of another.

// FeedbackRepository handles the read-only consumption of the user feedback
// persisted by the Python agent (feedback.db is owned by the agent since the
// AI unification — see docs/plano-unificacao-ia.md).
type FeedbackRepository interface {
	GetFalsePositiveComments(limit int) ([]string, error)
}

// AnomalyRepository handles persistence of moderation logs.
type AnomalyRepository interface {
	LogAnomaly(ref LiveRef, comment string, isAnomaly bool, category, uniqueID string) error
	GetRecentModerations(orgID string, limit int) ([]AnomalyLog, error)
	GetRecentAnomalyLogs(orgID string, limit int) ([]AnomalyLog, error)
	GetAnomalyLogsByLiveName(orgID, liveName string) ([]AnomalyLog, error)
	// GetAnomalyLogsByUser returns anomaly logs for a participant (case-insensitive).
	GetAnomalyLogsByUser(orgID, uniqueID string, limit int) ([]AnomalyLog, error)
	GetSessionAnomalyLogs(liveID string) ([]AnomalyLog, error)
	ClearHistory(orgID string) (int64, error)
	DeleteModeration(orgID string, id int64) (int64, error)
	CleanupOldAnomalies() (int64, error)
}

// UserMessageRepository handles persistence of user messages.
type UserMessageRepository interface {
	AddUserMessageDedup(ref LiveRef, uniqueID, username, message string) error
	GetUserMessages(orgID, uniqueID string) ([]UserMessage, error)
	// GetUserMessagesRecent returns the last `limit` messages of a user
	// (newest first).
	GetUserMessagesRecent(orgID, uniqueID string, limit int) ([]UserMessage, error)
	GetAllUserMessages(orgID string) (map[string][]UserMessage, error)
	GetSessionUserMessages(liveID string) ([]UserMessage, error)
}

// GiftRepository handles persistence of gifts.
type GiftRepository interface {
	AddGift(ref LiveRef, uniqueID, nickname, giftName string, repeatCount, giftType int) (int64, error)
	GetRecentGifts(orgID, liveName string, limit int) ([]Gift, error)
	GetGiftsByUser(orgID, uniqueID string) ([]Gift, error)
	GetGiftSummary(orgID string) (map[string]map[string]int, error)
	// GetGiftUnits returns total gift units (SUM repeat_count) and event count
	// for a session. When no gift names are given, all gifts count.
	GetGiftUnits(ref LiveRef, giftNames ...string) (units, count int, err error)
	ClearGifts(orgID string) (int64, error)
}

// TargetGiftHistoryRepository tracks target gift receive/answer history.
type TargetGiftHistoryRepository interface {
	AddTargetGiftHistory(ref LiveRef, uniqueID, nickname, giftName string, receivedAt time.Time, priority bool) (int64, error)
	// MarkTargetGiftAnswered returns ErrInvalidID when the entry does not
	// exist in the organization.
	MarkTargetGiftAnswered(orgID string, id int64, responseType string, answeredAt time.Time) error
	// SetTargetGiftPriority promotes (priority=true) or demotes (priority=false)
	// a pending entry in the gift queue. Promotion stamps `at` as the
	// promotion moment (FIFO among jumpers); demotion clears it.
	SetTargetGiftPriority(orgID string, id int64, priority bool, at time.Time) error
	GetRecentTargetGiftHistory(orgID, liveName string, limit int) ([]TargetGiftHistory, error)
	GetPendingTargetGiftHistory(orgID, liveName string, limit int) ([]TargetGiftHistory, error)
}

// GoalRepository handles persistence of live gift goals. Goals belong to a
// session: a goal created during a live is deleted with that live.
type GoalRepository interface {
	AddGiftGoal(g GiftGoal) (int64, error)
	GetGiftGoals(ref LiveRef) ([]GiftGoal, error)
	// SaveGiftGoal only updates the goal when it still belongs to g.LiveID.
	SaveGiftGoal(g GiftGoal) error
}

// PinnedCommentRepository tracks comments pinned during a live.
type PinnedCommentRepository interface {
	AddPinnedComment(ref LiveRef, uniqueID, nickname, comment, pinID string, isFollower *bool, at time.Time) (int64, error)
	GetRecentPinnedComments(orgID, liveName string, limit int) ([]PinnedComment, error)
}

// SessionRepository handles the lifecycle of monitoring sessions. A session is
// one connection of one organization to a live; live_name alone (the streamer
// username) is shared by every session of that streamer and cannot identify one.
type SessionRepository interface {
	// BeginLiveSession resumes an open, still-reusable session of the
	// organization for liveName or starts a new one. It never deletes data.
	BeginLiveSession(orgID, liveName string, now time.Time) (LiveSession, error)
	// EndLiveSession closes a session. Idempotent: closing twice keeps the
	// first ended_at instead of erroring.
	EndLiveSession(id string, at time.Time) error
	// TouchLiveSession refreshes last_seen_at (used by the resume rule).
	TouchLiveSession(id string, at time.Time) error
	GetLiveSession(id string) (LiveSession, error)
	// LatestLiveSession resolves a session of the organization from a
	// streamer name (open session first, then most recent). Used for events
	// that arrive without a liveId.
	LatestLiveSession(orgID, liveName string) (LiveSession, error)
	// DeleteLiveSession removes every row produced by one session and returns
	// the total number of rows deleted.
	DeleteLiveSession(id string) (int64, error)
}

// ShareRepository tracks social shares of the live made by participants.
type ShareRepository interface {
	AddShare(ref LiveRef, uniqueID, nickname string) error
	// GetUserShareCount returns the total number of share events made by a user.
	GetUserShareCount(orgID, uniqueID string) (int, error)
}

// LikeRepository tracks likes (hearts) sent by participants during a live.
type LikeRepository interface {
	AddLike(ref LiveRef, uniqueID, nickname string, likeCount int) error
	// GetUserLikeTotal returns the sum of like_count over all like events of a user.
	GetUserLikeTotal(orgID, uniqueID string) (int64, error)
	// UpsertRoomLikeTotal stores the room-level cumulative like counter as
	// reported by the stream (monotonic: only the highest value is kept).
	UpsertRoomLikeTotal(ref LiveRef, total int64) error
	// LikeTotals returns the room-level cumulative like total and the sum of
	// the per-event likes actually delivered by the stream for a live.
	LikeTotals(orgID, liveName string) (roomTotal, delivered int64, err error)
}

// RankingRepository handles engagement ranking and live analytics.
type RankingRepository interface {
	// LiveFirstSeen returns the first recorded timestamp for a live (RFC3339).
	LiveFirstSeen(orgID, liveName string) (string, error)
	// LiveStatsByUser returns per-user aggregated stats for a live.
	LiveStatsByUser(orgID, liveName string) ([]LiveStat, error)
	// RecentLivesForUser returns the last N lives a participant appeared in.
	RecentLivesForUser(orgID, uniqueID string, limit int) ([]UserLiveSummary, error)
	// TotalDistinctUsers counts distinct users across all user_messages.
	TotalDistinctUsers(orgID string) (int, error)
	// ListLives returns one row per session (live connection), most recent first.
	ListLives(orgID string, limit int) ([]Live, error)
}

// LiveStat is per-user aggregated data used to compute a ranking score.
type LiveStat struct {
	UniqueID      string
	Nickname      string
	MessageCount  int
	QuestionCount int
	GiftCount     int
	// GiftTotal is the sum of gift units (repeat_count) sent by the user.
	GiftTotal int
	// GiftValue is the total coin (diamond) value of the gifts sent by the
	// user: sum of GiftValue(gift_name) x repeat_count, using the researched
	// TikTok live gift price table.
	GiftValue  int
	ShareCount int
	LikeCount  int
	FirstSeen  string
	LastSeen   string
}

// OrganizationRepository stores tenants and their members.
type OrganizationRepository interface {
	CreateOrganization(name string, maxLives int) (Organization, error)
	GetOrganization(id string) (Organization, error)
	ListOrganizations() ([]Organization, error)
	UpdateOrganization(id string, name *string, maxLives *int, active *bool) (Organization, error)
	// GetMembership returns the organization of a user (ErrOrgNotFound when
	// the user belongs to none).
	GetMembership(userID string) (OrgMember, error)
	ListOrgMembers(orgID string) ([]OrgMember, error)
	// UpsertOrgMember assigns the user to orgID (a user belongs to exactly one
	// organization, so this moves the user when it was elsewhere).
	UpsertOrgMember(orgID, userID, email, role string) (OrgMember, error)
	DeleteOrgMember(orgID, userID string) (bool, error)
	// AssignLegacyLives moves sessions of the legacy organization (and so
	// every event row pointing at them) into a.OrgID, in one transaction.
	AssignLegacyLives(a LiveAssignment) (LiveAssignResult, error)
}

// PixSessionRepository stores the per-organization WAHA pairing state.
type PixSessionRepository interface {
	UpsertPixSession(orgID, sessionName, status, mePhone, meJID string, connectedAt *time.Time) (PixWhatsAppSession, error)
	GetPixSessionByOrg(orgID string) (PixWhatsAppSession, error)
	GetPixSessionByName(sessionName string) (PixWhatsAppSession, error)
}

// PixContactRepository stores WhatsApp counterparts of a Fila PIX organization.
type PixContactRepository interface {
	UpsertPixContact(orgID, phoneE164, jid, pushName string, at time.Time) (PixContact, error)
	GetPixContactByID(orgID string, id int64) (PixContact, error)
	SetPixContactName(orgID string, contactID int64, pushName string) error
}

// PixTicketRepository stores queue items and answered history.
type PixTicketRepository interface {
	GetPendingPixTicket(orgID string, contactID int64) (PixTicket, error)
	CreatePixTicket(orgID string, contactID int64, at time.Time) (int64, error)
	ListPixTickets(orgID, status string, limit int) ([]PixTicket, error)
	GetPixTicket(orgID string, id int64) (PixTicket, error)
	MarkPixTicketAnswered(orgID string, id int64, answeredBy string, at time.Time) error
	MarkPixTicketReceipt(orgID string, id int64, at time.Time) error
	ListPixContactHistory(orgID string, contactID int64, limit int) ([]PixTicket, error)
	MarkPixAutoReplySent(orgID string, ticketID int64, at time.Time) error
	// GetPixTicketPaidTotal sums the receipt values extracted so far for the
	// ticket, in cents (the value-gate accumulator).
	GetPixTicketPaidTotal(orgID string, ticketID int64) (int64, error)
	// DeleteEmptyPixTicket removes a pending ticket that has no messages at
	// all (a rejected receipt must not leave an empty row in the queue).
	DeleteEmptyPixTicket(orgID string, ticketID int64) (bool, error)
}

// PixMessageRepository stores messages and receipt media metadata.
type PixMessageRepository interface {
	// AddPixMessage returns the new id and true when inserted; false on dedup.
	AddPixMessage(m PixMessage) (int64, bool, error)
	ListPixMessages(orgID string, ticketID int64, limit int) ([]PixMessage, error)
	GetPixMessageForMedia(orgID string, id int64) (PixMessage, error)
	ListActivePixMedia(orgID string, limit int) ([]PixMessage, error)
	MarkPixMediaDeleted(orgID string, id int64, at time.Time, reason string) error
}

// SettingsRepository persists application settings (target gifts, moderation
// toggles, etc.) as key/value entries so they survive restarts.
type SettingsRepository interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

// PixValueRuleRepository stores the PIX amounts one organization accepts. A
// receipt only releases its ticket into the queue once the accumulated values
// equal one of these amounts (in cents).
type PixValueRuleRepository interface {
	ListPixValueRules(orgID string) ([]int64, error)
	ReplacePixValueRules(orgID string, cents []int64) error
}

// Repository combines all repository interfaces.
type Repository interface {
	FeedbackRepository
	AnomalyRepository
	UserMessageRepository
	GiftRepository
	ShareRepository
	LikeRepository
	TargetGiftHistoryRepository
	GoalRepository
	PinnedCommentRepository
	SessionRepository
	RankingRepository
	SettingsRepository
	OrganizationRepository
	PixSessionRepository
	PixContactRepository
	PixTicketRepository
	PixMessageRepository
	PixValueRuleRepository
	Close() error
}
