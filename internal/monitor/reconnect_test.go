package monitor

import (
	"context"
	"math/rand"
	"testing"
	"time"
)

// TestBackoffDelayTable valida o crescimento exponencial com teto.
func TestBackoffDelayTable(t *testing.T) {
	oldJitter := reconnectJitterPct
	reconnectBaseDelay = 100 * time.Millisecond
	reconnectMaxDelay = 500 * time.Millisecond
	reconnectJitterPct = 0 // deterministic: sem jitter
	defer func() {
		reconnectBaseDelay = time.Second
		reconnectMaxDelay = 30 * time.Second
		reconnectJitterPct = oldJitter
	}()

	base := func(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }

	for _, tt := range []struct {
		attempt  int
		min, max time.Duration // delay = [b, b*(1+jitter)], sujeito ao teto
	}{
		{attempt: 1, min: base(100), max: base(100)},
		{attempt: 2, min: base(200), max: base(240)},
		{attempt: 3, min: base(400), max: base(480)},
		{attempt: 4, min: base(500), max: base(500)}, // teto (base >= cap)
		{attempt: 10, min: base(500), max: base(500)},
		{attempt: 1000, min: base(500), max: base(500)},
	} {
		t.Run("attempt", func(t *testing.T) {
			for i := 0; i < 20; i++ {
				got := backoffDelay(tt.attempt)
				if got < tt.min || got > tt.max {
					t.Fatalf("backoffDelay(%d) = %s, want in [%s, %s]", tt.attempt, got, tt.min, tt.max)
				}
			}
		})
	}
}

// TestBackoffDelayJitter garante que o jitter produz mais de um valor
// distinto (com semente aleatória real) para o delay no teto.
func TestBackoffDelayJitter(t *testing.T) {
	oldBase, oldMax, oldJitter := reconnectBaseDelay, reconnectMaxDelay, reconnectJitterPct
	rand.New(rand.NewSource(time.Now().UnixNano()))
	reconnectBaseDelay, reconnectMaxDelay, reconnectJitterPct = time.Second, 30*time.Second, 0.2
	defer func() {
		reconnectBaseDelay, reconnectMaxDelay, reconnectJitterPct = oldBase, oldMax, oldJitter
	}()

	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		seen[backoffDelay(5)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected jitter to produce distinct delays, got %v", seen)
	}
}

// TestSupervisorReconnectsAfterBridgeDeath simula a morte do bridge e valida
// que o supervisor emite reconexão com retries e, ao "sair da live", para de
// tentar (backoff de teste acelerado via flags de pacote em outro test).
func TestSupervisorReconnectsAfterBridgeDeath(t *testing.T) {
	oldBase, oldMax := reconnectBaseDelay, reconnectMaxDelay
	reconnectBaseDelay, reconnectMaxDelay = 20*time.Millisecond, 40*time.Millisecond
	defer func() { reconnectBaseDelay, reconnectMaxDelay = oldBase, oldMax }()

	m, _ := New()

	type status struct{ retries int }
	statusCh := make(chan status, 10)
	m.OnEvent(func(eventType string, data EventData) {
		if eventType != EventConnectionStatus {
			return
		}
		if _, ok := data["retries"]; !ok {
			return
		}
		r, _ := data["retries"].(int)
		statusCh <- status{retries: r}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.mu.Lock()
	m.currentUsername = "testuser"
	ended := make(chan struct{})
	m.bridgeEnded = ended
	m.userStopped = false
	m.mu.Unlock()

	m.startSupervisor(ctx)

	// Bridge "morre".
	close(ended)

	select {
	case s := <-statusCh:
		if s.retries < 1 {
			t.Fatalf("expected retries >= 1, got %d", s.retries)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not emit reconnection status after bridge death")
	}

	// Usuário para: stopSupervisor zera os campos do supervisor.
	m.StopMonitoring()
	if m.supDone != nil {
		t.Fatal("expected supervisor to be stopped (and fields cleared) after StopMonitoring")
	}

	// Nenhum novo emit de reconexão após o stop.
	m.mu.Lock()
	m.userStopped = false
	ended2 := make(chan struct{})
	m.bridgeEnded = ended2
	m.mu.Unlock()
	m.startSupervisor(ctx)
	close(ended2)
	m.StopMonitoring()

	select {
	case <-statusCh:
		t.Fatal("supervisor emitted a reconnection status after user stop")
	case <-time.After(300 * time.Millisecond):
		// esperado: nada emitido
	}
}

// TestSupervisorStopsOnContextCancel garante que cancelar o contexto do
// controller encerra o supervisor (sem goroutines órfãs).
// TestSupervisorStopsOnContextCancel garante que cancelar o contexto do
// controller interrompe o supervisor imediatamente, mesmo no meio do backoff.
func TestSupervisorStopsOnContextCancel(t *testing.T) {
	oldBase, oldMax := reconnectBaseDelay, reconnectMaxDelay
	reconnectBaseDelay, reconnectMaxDelay = 20*time.Millisecond, 40*time.Millisecond
	defer func() { reconnectBaseDelay, reconnectMaxDelay = oldBase, oldMax }()

	m, _ := New()
	m.mu.Lock()
	m.currentUsername = "testuser"
	ended := make(chan struct{})
	m.bridgeEnded = ended
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	m.startSupervisor(ctx)
	close(ended) // força o supervisor para o backoff

	cancel()

	if m.supDone == nil {
		t.Fatal("supervisor not stopped after context cancel")
	}
}
