package monitor

import (
	"sync"
	"testing"
)

func TestTargetGiftQuantities(t *testing.T) {
	tests := []struct {
		name     string
		quantity int
		counts   []int
		want     []int
	}{
		{"legacy default", 0, []int{1, 3}, []int{1, 2}},
		{"separate sends", 2, []int{1, 1, 1, 1}, []int{0, 1, 1, 2}},
		{"combo and remainder", 2, []int{3, 1}, []int{1, 2}},
		{"large batch keeps remainder", 3, []int{8, 1}, []int{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New()
			if err != nil {
				t.Fatal(err)
			}
			m.SetSettings(Settings{TargetGifts: []string{"Rosa"}, TargetGiftQuantities: map[string]int{"Rosa": tt.quantity}})
			collector := &giftCollector{}
			m.OnEvent(collector.handler)
			for i, count := range tt.counts {
				m.handleTargetGift(EventData{"uniqueId": "ana", "giftName": "Rosa", "repeatCount": count, "repeatEnd": true})
				if _, got := collector.counts(); got != tt.want[i] {
					t.Fatalf("send %d: got %d target events, want %d", i, got, tt.want[i])
				}
			}
		})
	}
}

func TestTargetGiftProgressIsolation(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.SetSettings(Settings{TargetGifts: []string{"Rosa", "Dino"}, TargetGiftQuantities: map[string]int{"Rosa": 2, "Dino": 2}})
	m.SetCurrentLive("live1")
	steps := []struct {
		user, gift string
		want       bool
	}{
		{"ana", "Rosa", false}, {"bruno", "Rosa", false},
		{"ana", "Dino", false}, {"ana", "Rosa", true},
		{"bruno", "Rosa", true}, {"ana", "Dino", true},
	}
	for _, step := range steps {
		if got, _ := m.consumeTargetGift(step.user, step.gift, 1); got != step.want {
			t.Fatalf("%s/%s = %v, want %v", step.user, step.gift, got, step.want)
		}
	}
	m.consumeTargetGift("ana", "Rosa", 1)
	m.SetCurrentLive("LIVE1")
	if got, _ := m.consumeTargetGift("ana", "Rosa", 1); !got {
		t.Fatal("same live lost progress")
	}
	m.consumeTargetGift("ana", "Rosa", 1)
	m.SetCurrentLive("live2")
	if got, _ := m.consumeTargetGift("ana", "Rosa", 1); got {
		t.Fatal("new live reused progress")
	}
}

func TestTargetGiftSettingsChange(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "change quantity", true: "remove and readd"}[remove], func(t *testing.T) {
			m, err := New()
			if err != nil {
				t.Fatal(err)
			}
			m.SetSettings(Settings{TargetGifts: []string{"Rosa"}, TargetGiftQuantities: map[string]int{"Rosa": 3}})
			m.consumeTargetGift("ana", "Rosa", 2)
			if remove {
				m.SetSettings(Settings{})
			}
			m.SetSettings(Settings{TargetGifts: []string{"Rosa"}, TargetGiftQuantities: map[string]int{"Rosa": 2}})
			if got, _ := m.consumeTargetGift("ana", "Rosa", 1); got {
				t.Fatal("changed target retained progress")
			}
			m.SetSettings(Settings{LogLevel: "debug", TargetGifts: []string{"Rosa"}, TargetGiftQuantities: map[string]int{"Rosa": 2}})
			if got, _ := m.consumeTargetGift("ana", "Rosa", 1); !got {
				t.Fatal("unrelated settings lost progress")
			}
		})
	}
}

func TestTargetGiftComboCountsOnlySettlement(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.SetSettings(Settings{TargetGifts: []string{"Rosa"}, TargetGiftQuantities: map[string]int{"Rosa": 3}})
	collector := &giftCollector{}
	m.OnEvent(collector.handler)
	for _, count := range []int{1, 2} {
		m.handleTargetGift(EventData{"uniqueId": "ana", "giftName": "Rosa", "repeatCount": count, "giftType": 1, "repeatEnd": false})
	}
	m.handleTargetGift(EventData{"uniqueId": "Ana", "giftName": "Rosa", "repeatCount": 2, "giftType": 1, "repeatEnd": true})
	if _, got := collector.counts(); got != 0 {
		t.Fatal("intermediate combo updates were counted")
	}
	m.handleTargetGift(EventData{"uniqueId": "ana", "giftName": "Rosa", "repeatCount": 1, "giftType": 1, "repeatEnd": true})
	if _, got := collector.counts(); got != 1 {
		t.Fatal("settled gifts did not accumulate")
	}
}

func TestTargetGiftConcurrentAccumulation(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.SetSettings(Settings{TargetGifts: []string{"Rosa"}, TargetGiftQuantities: map[string]int{"Rosa": 2}})
	collector := &giftCollector{}
	m.OnEvent(collector.handler)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.handleTargetGift(EventData{"uniqueId": "ana", "giftName": "Rosa", "repeatCount": 1, "repeatEnd": true})
		}()
	}
	wg.Wait()
	if _, got := collector.counts(); got != 10 {
		t.Fatalf("got %d target events, want 10", got)
	}
}

func TestTargetGiftTimeoutDoesNotCountLateFinalTwice(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.SetSettings(Settings{TargetGifts: []string{"Rosa"}, TargetGiftQuantities: map[string]int{"Rosa": 2}})
	collector := &giftCollector{}
	m.OnEvent(collector.handler)
	data := EventData{"uniqueId": "ana", "giftName": "Rosa", "repeatCount": 1, "giftType": 1, "repeatEnd": false}
	key := giftStreakKey(data)
	m.giftStreaks[key] = &giftStreak{data: data, lastCount: 1}
	m.settleGiftStreak(key)
	m.handleSettledGiftUser(EventData{"uniqueId": "ana", "giftName": "Rosa", "repeatCount": 1, "giftType": 1, "repeatEnd": true})
	if any, target := collector.counts(); any != 1 || target != 0 {
		t.Fatalf("late final counted twice: any=%d target=%d", any, target)
	}
	if got, _ := m.consumeTargetGift("ana", "Rosa", 1); !got {
		t.Fatal("timeout lost accumulated gift")
	}
}

func TestTargetGiftPriorityFlag(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.SetSettings(Settings{
		TargetGifts:          []string{"Rosa", "Dino"},
		TargetGiftPriorities: map[string]bool{"Rosa": true},
	})
	collector := &giftCollector{}
	m.OnEvent(collector.handler)

	m.handleTargetGift(EventData{"uniqueId": "ana", "giftName": "Rosa", "repeatCount": 1, "repeatEnd": true})
	m.handleTargetGift(EventData{"uniqueId": "bia", "giftName": "Dino", "repeatCount": 1, "repeatEnd": true})

	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.target) != 2 {
		t.Fatalf("expected 2 target events, got %d", len(collector.target))
	}
	if got := collector.target[0]["isPriority"]; got != true {
		t.Fatalf("Rosa (fura fila): isPriority = %v, want true", got)
	}
	if got := collector.target[1]["isPriority"]; got != false {
		t.Fatalf("Dino (normal): isPriority = %v, want false", got)
	}
}
