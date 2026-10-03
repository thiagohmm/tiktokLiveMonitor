package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MonitorAttachmentStore tracks which users of each organization are watching
// each live. The monitor of a live belongs to one organization (one bridge per
// organization and live); the store decides when the organization's last user
// left and the monitor can be stopped.
type MonitorAttachmentStore struct {
	mu     sync.Mutex
	byUser map[attachUser]map[string]struct{}
	byLive map[attachLive]map[string]struct{}
}

type attachUser struct{ org, user string }
type attachLive struct{ org, live string }

// NewMonitorAttachmentStore creates an empty store.
func NewMonitorAttachmentStore() *MonitorAttachmentStore {
	return &MonitorAttachmentStore{
		byUser: make(map[attachUser]map[string]struct{}),
		byLive: make(map[attachLive]map[string]struct{}),
	}
}

// Attach adds the user of orgID to live and returns how many users of the
// organization now watch it.
func (s *MonitorAttachmentStore) Attach(orgID, userID, live string) int {
	orgID, userID, live = strings.TrimSpace(orgID), strings.TrimSpace(userID), strings.TrimSpace(live)
	if orgID == "" || userID == "" || live == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	u := attachUser{orgID, userID}
	if s.byUser[u] == nil {
		s.byUser[u] = make(map[string]struct{})
	}
	s.byUser[u][live] = struct{}{}
	l := attachLive{orgID, live}
	if s.byLive[l] == nil {
		s.byLive[l] = make(map[string]struct{})
	}
	s.byLive[l][userID] = struct{}{}
	return len(s.byLive[l])
}

// Detach removes the user from live and returns the organization's remaining
// watchers of that live.
func (s *MonitorAttachmentStore) Detach(orgID, userID, live string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detachLocked(strings.TrimSpace(orgID), strings.TrimSpace(userID), strings.TrimSpace(live))
}

func (s *MonitorAttachmentStore) detachLocked(orgID, userID, live string) int {
	if lives, ok := s.byUser[attachUser{orgID, userID}]; ok {
		delete(lives, live)
		if len(lives) == 0 {
			delete(s.byUser, attachUser{orgID, userID})
		}
	}
	l := attachLive{orgID, live}
	if users, ok := s.byLive[l]; ok {
		delete(users, userID)
		if len(users) == 0 {
			delete(s.byLive, l)
			return 0
		}
		return len(users)
	}
	return 0
}

// DetachAll removes the user from every live and returns the remaining
// watchers per live.
func (s *MonitorAttachmentStore) DetachAll(orgID, userID string) map[string]int {
	orgID, userID = strings.TrimSpace(orgID), strings.TrimSpace(userID)
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]int)
	lives := s.byUser[attachUser{orgID, userID}]
	for live := range lives {
		out[live] = s.detachLocked(orgID, userID, live)
	}
	delete(s.byUser, attachUser{orgID, userID})
	return out
}

// Watchers returns how many users of the organization watch a live.
func (s *MonitorAttachmentStore) Watchers(orgID, live string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byLive[attachLive{strings.TrimSpace(orgID), strings.TrimSpace(live)}])
}

// OrgWatchers returns how many users of the organization watch any live.
func (s *MonitorAttachmentStore) OrgWatchers(orgID string) int {
	orgID = strings.TrimSpace(orgID)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for u := range s.byUser {
		if u.org == orgID {
			n++
		}
	}
	return n
}

// AttachMonitoring registers the user as a watcher of the organization's live
// and starts the organization's monitor when this is the first watcher.
func (c *AppController) AttachMonitoring(ctx context.Context, orgID, userID, username string) error {
	username = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(username), "@")))
	if username == "" {
		return fmt.Errorf("username is required")
	}
	orgID = strings.TrimSpace(orgID)
	userID = strings.TrimSpace(userID)
	if orgID == "" || userID == "" {
		return fmt.Errorf("organization and user are required")
	}

	if validator, ok := c.repo.(interface {
		CheckAllowedLive(context.Context, string, string) error
	}); ok {
		if err := validator.CheckAllowedLive(ctx, orgID, username); err != nil {
			return err
		}
	}
	if watchers := c.attachments.Attach(orgID, userID, username); watchers > 1 {
		// Another member of the organization already runs this live.
		return nil
	}
	if err := c.StartMonitoring(ctx, orgID, username); err != nil {
		c.attachments.Detach(orgID, userID, username)
		return err
	}
	return nil
}

// DetachMonitoring removes the user from one live (or all) and returns the
// lives it was detached from. The organization's monitor is stopped only when
// its last watcher leaves; when nobody of the organization watches any live,
// its Fila PIX receipts are purged in the background.
func (c *AppController) DetachMonitoring(orgID, userID, username string) []string {
	orgID = strings.TrimSpace(orgID)
	userID = strings.TrimSpace(userID)
	username = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(username), "@")))
	if orgID == "" || userID == "" {
		return nil
	}

	remaining := make(map[string]int)
	lives := make([]string, 0, 1)
	if username != "" {
		remaining[username] = c.attachments.Detach(orgID, userID, username)
		lives = append(lives, username)
	} else {
		remaining = c.attachments.DetachAll(orgID, userID)
		for live := range remaining {
			lives = append(lives, live)
		}
	}

	for live, watchers := range remaining {
		if watchers == 0 {
			c.monitorManager.StopMonitoring(orgID, live)
		}
	}

	if c.pixQueue != nil && c.pixQueue.Enabled() && c.attachments.OrgWatchers(orgID) == 0 {
		go func() {
			if _, err := c.pixQueue.PurgeOrgMedia(context.Background(), orgID, "live disconnected"); err != nil {
				logPix("purge organization media: %v", err)
			}
		}()
	}
	return lives
}

// DetachUser removes a user from every live of the organization (e.g. when the
// member is removed), stopping lives that lose their last watcher.
func (c *AppController) DetachUser(orgID, userID string) {
	c.DetachMonitoring(orgID, userID, "")
}

// StopOrgMonitoring stops every live of the organization and forgets its
// watchers (used when the organization is disabled).
func (c *AppController) StopOrgMonitoring(orgID string) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return
	}
	c.attachments.DropOrg(orgID)
	c.monitorManager.StopMonitoring(orgID, "")
}

// DropOrg forgets every watcher of the organization.
func (s *MonitorAttachmentStore) DropOrg(orgID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for u := range s.byUser {
		if u.org == orgID {
			delete(s.byUser, u)
		}
	}
	for l := range s.byLive {
		if l.org == orgID {
			delete(s.byLive, l)
		}
	}
}
