package controller

import (
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

type settingsRepository struct {
	model.Repository
	values map[string]string
}

func newSettingsRepository() *settingsRepository {
	return &settingsRepository{values: map[string]string{}}
}

func (r *settingsRepository) GetSetting(key string) (string, error) { return r.values[key], nil }
func (r *settingsRepository) SetSetting(key, value string) error {
	r.values[key] = value
	return nil
}

func newSettingsController(t *testing.T, repo model.Repository) *AppController {
	t.Helper()
	mon, err := monitor.New()
	if err != nil {
		t.Fatal(err)
	}
	return NewAppController(mon, repo)
}

func TestTargetGiftSettingsPersistAndRestore(t *testing.T) {
	repo := newSettingsRepository()
	ctrl := newSettingsController(t, repo)
	if err := ctrl.SetSettings(testOrgID, monitor.Settings{TargetGifts: []string{"Rosa"}, TargetGiftQuantities: map[string]int{"Rosa": 2}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.values[database.OrgSettingsKey(testOrgID)]; !ok {
		t.Fatalf("settings not stored under the organization key: %v", repo.values)
	}
	restored := newSettingsController(t, repo).GetSettings(testOrgID)
	if len(restored.TargetGifts) != 1 || restored.TargetGifts[0] != "Rosa" || restored.TargetGiftQuantities["Rosa"] != 2 {
		t.Fatalf("settings not restored: %+v", restored)
	}
}

func TestSettingsAreIsolatedPerOrg(t *testing.T) {
	repo := newSettingsRepository()
	ctrl := newSettingsController(t, repo)
	if err := ctrl.SetSettings("org-a", monitor.Settings{TargetGifts: []string{"Rosa"}, ModerationEnabled: false}); err != nil {
		t.Fatal(err)
	}

	b := ctrl.GetSettings("org-b")
	if len(b.TargetGifts) != 0 || !b.ModerationEnabled {
		t.Fatalf("org-b must keep the defaults, got %+v", b)
	}
	a := ctrl.GetSettings("org-a")
	if len(a.TargetGifts) != 1 || a.ModerationEnabled {
		t.Fatalf("org-a settings lost: %+v", a)
	}

	// A fresh controller reads each organization from its own key.
	fresh := newSettingsController(t, repo)
	if got := fresh.GetSettings("org-b"); len(got.TargetGifts) != 0 {
		t.Fatalf("org-b restored org-a's settings: %+v", got)
	}
	if got := fresh.GetSettings("org-a"); len(got.TargetGifts) != 1 {
		t.Fatalf("org-a not restored: %+v", got)
	}

	if err := ctrl.SetSettings("  ", monitor.Settings{}); err == nil {
		t.Fatal("expected an error for settings without organization")
	}
}
