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
	Progress               GoalProgress          `json:"progress"`
	UnlockedMilestones     []model.GoalMilestone `json:"unlockedMilestones,omitempty"`
	NewlyUnlockedMilestones []model.GoalMilestone `json:"newlyUnlockedMilestones,omitempty"`
	Completed              bool                  `json:"completed"`
}

// GoalsState is the full view of goals for the current live. Multiple goals
// can be active at once; Actives lists all of them with their progress.
type GoalsState struct {
	LiveName string           `json:"liveName"`
	Active   *GoalProgress    `json:"active"` // legacy alias: the first active goal
	Actives  []GoalProgress   `json:"actives"`
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

// CreateGoal stores a new active goal for the current live. Multiple goals
// may be active at the same time: each one is tracked independently against
// the live's units and completed when its own target is met.
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

// CancelGoal marks the given active goal of the current live as cancelled.
func (c *AppController) CancelGoal(id int64) error {
	g, err := c.activeGoalByID(id)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("meta não encontrada")
	}
	g.Status = model.GoalStatusCancelled
	return c.repo.SaveGiftGoal(*g)
}

// CompleteGoal marks the given active goal of the current live as completed,
// unlocking any milestone already crossed by the current units.
func (c *AppController) CompleteGoal(id int64) error {
	g, err := c.activeGoalByID(id)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("meta não encontrada")
	}
	units, _, err := c.repo.GetGiftUnits(g.LiveName, goalGiftNames(g.GiftName)...)
	if err != nil {
		return err
	}
	c.crossMilestones(g, units)
	now := time.Now().UTC().Format(time.RFC3339)
	g.Status = model.GoalStatusCompleted
	g.CompletedAt = &now
	return c.repo.SaveGiftGoal(*g)
}

// activeGoalByID returns the current live's active goal with the given id,
// or nil when there is no such active goal.
func (c *AppController) activeGoalByID(id int64) (*model.GiftGoal, error) {
	liveName := c.monitor.GetState().Username
	goals, err := c.repo.GetGiftGoals(liveName)
	if err != nil {
		return nil, err
	}
	for i := range goals {
		if goals[i].ID == id && goals[i].Status == model.GoalStatusActive {
			return &goals[i], nil
		}
	}
	return nil, nil
}

// GetGoalsState returns the current live's active goals (each with progress),
// its goal history, and the legacy Active alias (first active goal).
func (c *AppController) GetGoalsState() (GoalsState, error) {
	liveName := c.monitor.GetState().Username
	out := GoalsState{LiveName: liveName, Actives: []GoalProgress{}, History: []model.GiftGoal{}}
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
		out.Actives = append(out.Actives, GoalProgress{
			Goal:    g,
			Units:   units,
			Percent: progressPercent(units, g.TargetUnits),
		})
	}
	if len(out.Actives) > 0 {
		out.Active = &out.Actives[0]
	}
	return out, nil
}

// checkGoalProgress recomputes the live's units and, for every active goal,
// unlocks crossed milestones and completes the goal when its target is met.
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
	goals, err := c.repo.GetGiftGoals(liveName)
	if err != nil {
		log.Printf("[Controller] Error reading goals: %v", err)
		return
	}
	for i := range goals {
		if goals[i].Status != model.GoalStatusActive {
			continue
		}
		c.checkSingleGoal(&goals[i])
	}
}

// checkSingleGoal advances one active goal: crossed milestones, completion at
// target, persistence and the goal-update emission.
func (c *AppController) checkSingleGoal(active *model.GiftGoal) {
	liveName := active.LiveName
	// An empty GiftName counts every gift; a per-gift goal only counts its gift.
	units, _, err := c.repo.GetGiftUnits(liveName, goalGiftNames(active.GiftName)...)
	if err != nil {
		log.Printf("[Controller] Error reading gift units: %v", err)
		return
	}

	changed, newlyUnlocked := c.crossMilestones(active, units)
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
		c.emitGoalUpdate(*active, units, completedNow, newlyUnlocked)
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
// units; it reports whether anything changed and which milestones were newly unlocked.
func (c *AppController) crossMilestones(active *model.GiftGoal, units int) (bool, []model.GoalMilestone) {
	changed := false
	newlyUnlocked := make([]model.GoalMilestone, 0)
	for i := range active.Milestones {
		m := &active.Milestones[i]
		if m.Unlocked || m.AtUnits <= 0 || units < m.AtUnits {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339)
		m.Unlocked = true
		m.UnlockedAt = &now
		changed = true
		newlyUnlocked = append(newlyUnlocked, *m)
	}
	return changed, newlyUnlocked
}

// emitGoalUpdate fires the goal callback (if registered) with the new state.
func (c *AppController) emitGoalUpdate(goal model.GiftGoal, units int, completedNow bool, newlyUnlocked []model.GoalMilestone) {
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
		UnlockedMilestones:      unlocked,
		NewlyUnlockedMilestones: newlyUnlocked,
		Completed:               completedNow,
	})
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
