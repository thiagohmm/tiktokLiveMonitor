package controller

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// GoalProgress describes a goal together with the live's current units.
type GoalProgress struct {
	Goal    model.GiftGoal `json:"goal"`
	Units   int            `json:"units"`
	Percent float64        `json:"percent"`
}

// GoalUpdate is emitted through the goal callback when progress changes:
// crossed milestones and/or completion of the active goal.
type GoalUpdate struct {
	Progress           GoalProgress          `json:"progress"`
	UnlockedMilestones []model.GoalMilestone `json:"unlockedMilestones,omitempty"`
	Completed          bool                  `json:"completed"`
}

// GoalsState is the full view of goals for the current live.
type GoalsState struct {
	LiveName string           `json:"liveName"`
	Active   *GoalProgress    `json:"active"`
	History  []model.GiftGoal `json:"history"`
}

// goalState holds the in-memory goal bookkeeping for the controller.
// The AppController struct is defined in app.go; these fields live here so
// the goal logic stays self-contained.
type goalState struct {
	mu sync.Mutex
	// callback is invoked when goal progress changes.
	callback func(GoalUpdate)
	// lastUnits tracks the last units value emitted per goal id, so a gift
	// that only moves the progress bar (no milestone, no completion) still
	// triggers a goal-update.
	lastUnits map[int64]int
}

// SetGoalCallback registers the callback invoked when goal progress changes.
func (c *AppController) SetGoalCallback(fn func(GoalUpdate)) {
	c.goals.mu.Lock()
	defer c.goals.mu.Unlock()
	c.goals.callback = fn
}

// CreateGoal stores a new active goal for the current live. If an active goal
// already exists it is cancelled first (one active goal per live).
// An empty giftName counts all gifts; otherwise only that gift counts.
func (c *AppController) CreateGoal(title, giftName string, targetUnits int, milestones []model.GoalMilestone) (model.GiftGoal, error) {
	state := c.monitor.GetState()
	liveName := state.Username
	if liveName == "" {
		return model.GiftGoal{}, fmt.Errorf("no live is being monitored")
	}
	if len(milestones) == 0 {
		milestones = []model.GoalMilestone{}
	}

	active, err := c.activeGoal(liveName)
	if err != nil {
		return model.GiftGoal{}, err
	}
	if active != nil {
		active.Status = model.GoalStatusCancelled
		if err := c.repo.SaveGiftGoal(*active); err != nil {
			return model.GiftGoal{}, fmt.Errorf("cancel previous goal: %w", err)
		}
	}

	g := model.GiftGoal{
		LiveName:    liveName,
		Title:       title,
		GiftName:    giftName,
		TargetUnits: targetUnits,
		Status:      model.GoalStatusActive,
		Milestones:  milestones,
	}
	id, err := c.repo.AddGiftGoal(g)
	if err != nil {
		return model.GiftGoal{}, err
	}
	g.ID = id
	return g, nil
}

// UpdateGoal persists mutable fields of an existing goal.
func (c *AppController) UpdateGoal(g model.GiftGoal) error {
	if g.ID <= 0 {
		return fmt.Errorf("invalid goal id")
	}
	if g.Status == "" {
		g.Status = model.GoalStatusActive
	}
	return c.repo.SaveGiftGoal(g)
}

// CancelGoal marks the current live's active goal as cancelled.
func (c *AppController) CancelGoal() error {
	liveName := c.monitor.GetState().Username
	active, err := c.activeGoal(liveName)
	if err != nil {
		return err
	}
	if active == nil {
		return fmt.Errorf("no active goal")
	}
	active.Status = model.GoalStatusCancelled
	return c.repo.SaveGiftGoal(*active)
}

// CompleteGoal marks the current live's active goal as completed,
// unlocking any milestone already crossed by the current units.
func (c *AppController) CompleteGoal() error {
	liveName := c.monitor.GetState().Username
	active, err := c.activeGoal(liveName)
	if err != nil {
		return err
	}
	if active == nil {
		return fmt.Errorf("no active goal")
	}
	units, _, err := c.repo.GetGiftUnits(liveName, goalGiftNames(active.GiftName)...)
	if err != nil {
		return err
	}
	c.crossMilestones(active, units)
	now := time.Now().UTC().Format(time.RFC3339)
	active.Status = model.GoalStatusCompleted
	active.CompletedAt = &now
	return c.repo.SaveGiftGoal(*active)
}

// GetGoalsState returns the current live's active goal (with progress) plus
// its goal history.
func (c *AppController) GetGoalsState() (GoalsState, error) {
	liveName := c.monitor.GetState().Username
	out := GoalsState{LiveName: liveName, History: []model.GiftGoal{}}
	if liveName == "" {
		return out, nil
	}
	goals, err := c.repo.GetGiftGoals(liveName)
	if err != nil {
		return GoalsState{}, err
	}
	for i := range goals {
		g := goals[i]
		if g.Status != model.GoalStatusActive {
			out.History = append(out.History, g)
			continue
		}
		units, _, err := c.repo.GetGiftUnits(liveName, goalGiftNames(g.GiftName)...)
		if err != nil {
			return GoalsState{}, err
		}
		out.Active = &GoalProgress{
			Goal:    g,
			Units:   units,
			Percent: progressPercent(units, g.TargetUnits),
		}
	}
	return out, nil
}

// checkGoalProgress recomputes the live's units and, for the active goal,
// unlocks crossed milestones and completes the goal when the target is met.
// It is called at the end of HandleGiftEvent.
func (c *AppController) checkGoalProgress() {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[Controller] panic checking goal progress: %v", rec)
		}
	}()

	liveName := c.monitor.GetState().Username
	if liveName == "" {
		return
	}
	active, err := c.activeGoal(liveName)
	if err != nil || active == nil {
		return
	}
	// An empty GiftName counts every gift; a per-gift goal only counts its gift.
	units, _, err := c.repo.GetGiftUnits(liveName, goalGiftNames(active.GiftName)...)
	if err != nil {
		log.Printf("[Controller] Error reading gift units: %v", err)
		return
	}

	changed := c.crossMilestones(active, units)
	completedNow := false
	if active.Status == model.GoalStatusActive && units >= active.TargetUnits {
		now := time.Now().UTC().Format(time.RFC3339)
		active.Status = model.GoalStatusCompleted
		active.CompletedAt = &now
		completedNow = true
		changed = true
	}
	if changed {
		if err := c.repo.SaveGiftGoal(*active); err != nil {
			log.Printf("[Controller] Error saving goal progress: %v", err)
		}
	}
	// Emit whenever the units moved, so the UI progress bar/percent tracks
	// every gift even without milestones or completion.
	if c.unitsChanged(active.ID, units) || completedNow {
		c.emitGoalUpdate(*active, units, completedNow)
	}
}

// unitsChanged reports whether the units value differs from the last one
// emitted for the goal (or was never emitted). It updates the tracker, so
// repeat checks with unchanged units stay silent.
func (c *AppController) unitsChanged(goalID int64, units int) bool {
	c.goals.mu.Lock()
	defer c.goals.mu.Unlock()
	last, ok := c.goals.lastUnits[goalID]
	if ok && last == units {
		return false
	}
	if len(c.goals.lastUnits) > 128 {
		c.goals.lastUnits = make(map[int64]int)
	}
	c.goals.lastUnits[goalID] = units
	return true
}

// crossMilestones unlocks not-yet-unlocked milestones crossed by the given
// units; it reports whether anything changed.
func (c *AppController) crossMilestones(active *model.GiftGoal, units int) bool {
	changed := false
	for i := range active.Milestones {
		m := &active.Milestones[i]
		if m.Unlocked || m.AtUnits <= 0 || units < m.AtUnits {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339)
		m.Unlocked = true
		m.UnlockedAt = &now
		changed = true
	}
	return changed
}

// emitGoalUpdate fires the goal callback (if registered) with the new state.
func (c *AppController) emitGoalUpdate(goal model.GiftGoal, units int, completedNow bool) {
	c.goals.mu.Lock()
	fn := c.goals.callback
	c.goals.mu.Unlock()
	if fn == nil {
		return
	}

	unlocked := make([]model.GoalMilestone, 0)
	for _, m := range goal.Milestones {
		if m.Unlocked {
			unlocked = append(unlocked, m)
		}
	}
	fn(GoalUpdate{
		Progress: GoalProgress{
			Goal:    goal,
			Units:   units,
			Percent: progressPercent(units, goal.TargetUnits),
		},
		UnlockedMilestones: unlocked,
		Completed:          completedNow,
	})
}

// activeGoal returns the current live's active goal, or nil when there is none.
func (c *AppController) activeGoal(liveName string) (*model.GiftGoal, error) {
	goals, err := c.repo.GetGiftGoals(liveName)
	if err != nil {
		return nil, err
	}
	for i := range goals {
		if goals[i].Status == model.GoalStatusActive {
			return &goals[i], nil
		}
	}
	return nil, nil
}

// goalGiftNames returns the DB gift_name values that can hold the goal's
// gift: the original name (as chosen in the UI) plus its PT-BR translation,
// since gifts are persisted under the translated name.
func goalGiftNames(giftName string) []string {
	if strings.TrimSpace(giftName) == "" {
		return nil
	}
	names := []string{giftName}
	if translated := translateGiftName(giftName); translated != "" && translated != giftName {
		names = append(names, translated)
	}
	return names
}

// progressPercent clamps units/target to [0, 100].
func progressPercent(units, target int) float64 {
	if target < 1 {
		return 0
	}
	p := float64(units) / float64(target) * 100
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
