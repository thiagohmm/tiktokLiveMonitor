package monitor

import (
	"sync"
	"testing"
	"time"
)

type giftCollector struct {
	mu     sync.Mutex
	any    []EventData
	target []EventData
}

func (c *giftCollector) handler(eventType string, data EventData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch eventType {
	case EventAnyGift:
		c.any = append(c.any, data)
	case EventGiftUser:
		c.target = append(c.target, data)
	}
}

func (c *giftCollector) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.any), len(c.target)
}

func withFastStreakTimeout(t *testing.T) {
	t.Helper()
	oldSettle, oldKeep := giftStreakSettleTimeout.Load(), giftStreakKeepAfterSettle.Load()
	giftStreakSettleTimeout.Store((60 * time.Millisecond).Nanoseconds())
	giftStreakKeepAfterSettle.Store(time.Hour.Nanoseconds())
	t.Cleanup(func() {
		giftStreakSettleTimeout.Store(oldSettle)
		giftStreakKeepAfterSettle.Store(oldKeep)
	})
}

func TestGiftStreakHeldUntilFinalEvent(t *testing.T) {
	withFastStreakTimeout(t)
	m, _ := New()
	m.SetSettings(Settings{TargetGifts: []string{"Rosa"}})
	col := &giftCollector{}
	m.OnEvent(col.handler)

	m.handleBridgeEvent(EventAnyGift, EventData{
		"uniqueId": "u1", "giftId": "5832", "giftName": "Rosa",
		"repeatCount": float64(1), "repeatEnd": false,
	})
	if n, _ := col.counts(); n != 0 {
		t.Fatalf("evento intermediário não deve ser emitido antes da liquidação, got %d", n)
	}

	m.handleBridgeEvent(EventAnyGift, EventData{
		"uniqueId": "u1", "giftId": "5832", "giftName": "Rosa",
		"repeatCount": float64(3), "repeatEnd": true,
	})
	// Final: any-gift emitido imediatamente; new-gift-user separado do bridge
	// também precisa chegar para o fluxo de alvo.
	m.handleBridgeEvent(EventGiftUser, EventData{
		"uniqueId": "u1", "giftId": "5832", "giftName": "Rosa",
		"repeatCount": float64(3), "repeatEnd": true,
	})
	if n, target := col.counts(); n != 1 || target != 1 {
		t.Fatalf("esperava 1 any + 1 target, got %d/%d", n, target)
	}
}

func TestGiftStreakSettledByTimeoutWhenFinalLost(t *testing.T) {
	withFastStreakTimeout(t)
	m, _ := New()
	m.SetSettings(Settings{TargetGifts: []string{"Heart Me"}})
	col := &giftCollector{}
	m.OnEvent(col.handler)

	m.handleBridgeEvent(EventAnyGift, EventData{
		"uniqueId": "heloisa", "giftId": "7934", "giftName": "Heart Me",
		"repeatCount": float64(2), "repeatEnd": false,
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if n, target := col.counts(); n == 1 && target == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("streak não foi liquidado pelo timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	col.mu.Lock()
	data := col.any[0]
	col.mu.Unlock()
	if !truthy(data["repeatEnd"]) || int(data["repeatCount"].(int)) != 2 {
		t.Fatalf("liquidação incorreta: %+v", data)
	}
}

func TestLateFinalSuppressedAfterTimeoutSettle(t *testing.T) {
	withFastStreakTimeout(t)
	m, _ := New()
	m.SetSettings(Settings{TargetGifts: []string{"Rosa"}})
	col := &giftCollector{}
	m.OnEvent(col.handler)

	m.handleBridgeEvent(EventAnyGift, EventData{
		"uniqueId": "u9", "giftId": "5832", "giftName": "Rosa",
		"repeatCount": float64(1), "repeatEnd": false,
	})
	time.Sleep(300 * time.Millisecond)
	if n, target := col.counts(); n != 1 || target != 1 {
		t.Fatalf("esperava liquidação por timeout (1/1), got %d/%d", n, target)
	}

	// Final atrasado: não pode gravar de novo nem re-registrar o alvo.
	m.handleBridgeEvent(EventAnyGift, EventData{
		"uniqueId": "u9", "giftId": "5832", "giftName": "Rosa",
		"repeatCount": float64(4), "repeatEnd": true,
	})
	m.handleBridgeEvent(EventGiftUser, EventData{
		"uniqueId": "u9", "giftId": "5832", "giftName": "Rosa",
		"repeatCount": float64(4), "repeatEnd": true,
	})
	time.Sleep(150 * time.Millisecond)
	if n, target := col.counts(); n != 1 || target != 1 {
		t.Fatalf("final atrasado deve ser suprimido, got %d/%d", n, target)
	}
}

func TestSingleGiftWithRepeatEndEmitsImmediately(t *testing.T) {
	withFastStreakTimeout(t)
	m, _ := New()
	col := &giftCollector{}
	m.OnEvent(col.handler)

	m.handleBridgeEvent(EventAnyGift, EventData{
		"uniqueId": "u5", "giftId": "1", "giftName": "Rosa",
		"repeatCount": float64(1), "repeatEnd": true,
	})
	if n, _ := col.counts(); n != 1 {
		t.Fatalf("presente liquidado deve ser emitido na hora, got %d", n)
	}
}
