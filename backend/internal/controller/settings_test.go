package controller

import (
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

type settingsRepository struct {
	model.Repository
	value string
}

func (r *settingsRepository) GetSetting(string) (string, error) { return r.value, nil }
func (r *settingsRepository) SetSetting(_ string, value string) error {
	r.value = value
	return nil
}

func TestTargetGiftSettingsPersistAndRestore(t *testing.T) {
	repo := &settingsRepository{}
	mon, err := monitor.New()
	if err != nil {
		t.Fatal(err)
	}
	ctrl := NewAppController(mon, repo)
	ctrl.SetSettings(monitor.Settings{TargetGifts: []string{"Rosa"}, TargetGiftQuantities: map[string]int{"Rosa": 2}})
	restoredMonitor, err := monitor.New()
	if err != nil {
		t.Fatal(err)
	}
	restored := NewAppController(restoredMonitor, repo).GetSettings()
	if len(restored.TargetGifts) != 1 || restored.TargetGifts[0] != "Rosa" || restored.TargetGiftQuantities["Rosa"] != 2 {
		t.Fatalf("settings not restored: %+v", restored)
	}
}
