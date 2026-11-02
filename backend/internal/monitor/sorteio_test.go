package monitor

import (
	"errors"
	"testing"
	"time"
)

type capturedEvent struct {
	eventType string
	data      EventData
}

func newSorteioMonitor(t *testing.T, keyword string) (*Monitor, *[]capturedEvent) {
	t.Helper()
	m, _ := New()
	m.SetSettings(Settings{SorteioKeyword: keyword})
	events := &[]capturedEvent{}
	m.OnEvent(func(eventType string, data EventData) {
		*events = append(*events, capturedEvent{eventType, data})
	})
	return m, events
}

// A comment containing the keyword (accent/case-insensitive) joins the raffle
// exactly once per user, and only while collection is active.
func TestSorteioCollectsKeywordComments(t *testing.T) {
	m, events := newSorteioMonitor(t, "Sorteio")
	m.StartSorteio()

	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "User1", "nickname": "User One", "comment": "participando do SORTEIO!"})
	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "user1", "nickname": "User One", "comment": "quero o sorteio de novo"})
	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "User2", "nickname": "User Two", "comment": "boa noite a todos"})

	state := m.SorteioSnapshot()
	if len(state.Participants) != 1 {
		t.Fatalf("expected 1 participant, got %#v", state.Participants)
	}
	if state.Participants[0].UniqueID != "User1" || state.Participants[0].Nickname != "User One" {
		t.Fatalf("unexpected first participant: %#v", state.Participants[0])
	}
	if !state.Active {
		t.Fatal("collection should be active after StartSorteio")
	}

	sorteioParticipants := 0
	for _, ev := range *events {
		if ev.eventType == EventSorteioParticipant {
			sorteioParticipants++
		}
	}
	if sorteioParticipants != 1 {
		t.Fatalf("expected 1 sorteio-participant event, got %d", sorteioParticipants)
	}

	// Coleta desligada não adiciona.
	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "User4", "nickname": "U4", "comment": "sem palavra aqui"})
	if got := len(m.SorteioSnapshot().Participants); got != 1 {
		t.Fatalf("inactive collection must not add participants, got %d", got)
	}

	m.StartSorteio()
	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "User4", "nickname": "U4", "comment": "sorteio"})
	if got := len(m.SorteioSnapshot().Participants); got != 2 {
		t.Fatalf("expected active collection to add User4, got %d", got)
	}

	m.StopSorteio()
	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "User5", "nickname": "U5", "comment": "sorteio"})
	if got := len(m.SorteioSnapshot().Participants); got != 2 {
		t.Fatalf("stopped collection must not add participants, got %d", got)
	}
}

// Palavra-chave com acento casa com comentário sem acento (foldText).
func TestSorteioKeywordFoldAccents(t *testing.T) {
	m, _ := newSorteioMonitor(t, "Participação")
	m.StartSorteio()
	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "User1", "comment": "PARTICIPACAO quero entrar"})
	if got := len(m.SorteioSnapshot().Participants); got != 1 {
		t.Fatalf("expected accented keyword to match folded comment, got %#v", m.SorteioSnapshot())
	}
}

func TestSorteioWithoutKeywordCollectsNothing(t *testing.T) {
	m, _ := newSorteioMonitor(t, "")
	m.StartSorteio()
	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "User1", "comment": "sorteio"})
	if got := len(m.SorteioSnapshot().Participants); got != 0 {
		t.Fatalf("expected no participants without keyword, got %d", got)
	}
}

func TestSorteioManualParticipants(t *testing.T) {
	m, events := newSorteioMonitor(t, "sorteio")

	p, added, err := m.AddSorteioManualParticipant("@Maria_Souza")
	if err != nil || !added {
		t.Fatalf("add manual: added=%v err=%v", added, err)
	}
	if !p.Manual || p.Nickname != "Maria_Souza" {
		t.Fatalf("unexpected manual participant: %#v", p)
	}
	// Mesmo nome (caixa/espacos/@ diferentes) não entra duas vezes.
	_, addedAgain, err := m.AddSorteioManualParticipant("  maria_souza ")
	if err != nil || addedAgain {
		t.Fatalf("expected dedupe, added=%v err=%v", addedAgain, err)
	}
	if got := len(m.SorteioSnapshot().Participants); got != 1 {
		t.Fatalf("expected 1 manual participant, got %d", got)
	}

	if _, _, err := m.AddSorteioManualParticipant("   "); !errors.Is(err, ErrSorteioEmptyName) {
		t.Fatalf("expected empty-name error, got %v", err)
	}
	if _, _, err := m.AddSorteioManualParticipant(string(make([]byte, 60))); err == nil {
		t.Fatal("expected oversized name error")
	}

	// Um usuário do chat pode ser removido por id; manual por nome.
	if _, _, err := m.AddSorteioManualParticipant("João"); err != nil {
		t.Fatalf("add João: %v", err)
	}
	if !m.RemoveSorteioParticipant("joão") {
		t.Fatal("expected manual removal by normalized name")
	}
	if !m.RemoveSorteioParticipant("MARIA_SOUZA") {
		t.Fatal("expected manual removal of Maria")
	}
	if got := len(m.SorteioSnapshot().Participants); got != 0 {
		t.Fatalf("expected empty pool, got %d", got)
	}

	manualEvents := 0
	for _, ev := range *events {
		if ev.eventType == EventSorteioParticipant {
			manualEvents++
		}
	}
	if manualEvents != 2 {
		t.Fatalf("expected 2 manual participant events, got %d", manualEvents)
	}
}

func TestSorteioDrawRemovesWinnersAndKeepsRest(t *testing.T) {
	m, events := newSorteioMonitor(t, "sorteio")
	m.StartSorteio()
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": name, "nickname": name, "comment": "sorteio"})
	}

	winners, err := m.DrawSorteio(2)
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	if len(winners) != 2 {
		t.Fatalf("expected 2 winners, got %d", len(winners))
	}
	seen := map[string]bool{}
	for _, w := range winners {
		if seen[w.UniqueID] {
			t.Fatalf("duplicate winner in the same draw: %v", winners)
		}
		seen[w.UniqueID] = true
	}
	state := m.SorteioSnapshot()
	if len(state.Participants) != 3 {
		t.Fatalf("expected 3 suplentes, got %d", len(state.Participants))
	}
	for _, w := range winners {
		for _, p := range state.Participants {
			if p.UniqueID == w.UniqueID {
				t.Fatalf("winner %s must leave the pool", w.UniqueID)
			}
		}
	}
	if len(state.Winners) != 2 {
		t.Fatalf("expected last result kept, got %#v", state.Winners)
	}

	// Pool insuficiente: 3 suplentes, 4 pedidos.
	if _, err := m.DrawSorteio(4); !errors.Is(err, ErrSorteioInsufficientPool) {
		t.Fatalf("expected insufficient pool error, got %v", err)
	}
	// Suplentes: próximos 3 saem, pool fica vazio.
	suplentes, err := m.DrawSorteio(3)
	if err != nil || len(suplentes) != 3 {
		t.Fatalf("suplentes draw: err=%v winners=%v", err, suplentes)
	}
	if got := m.SorteioSnapshot().Participants; len(got) != 0 {
		t.Fatalf("expected empty pool, got %v", got)
	}

	resultEvents := 0
	for _, ev := range *events {
		if ev.eventType == EventSorteioResult {
			resultEvents++
		}
	}
	if resultEvents != 2 {
		t.Fatalf("expected 2 sorteio-result events, got %d", resultEvents)
	}
}

func TestSorteioResetClearsParticipantsAndResult(t *testing.T) {
	m, _ := newSorteioMonitor(t, "sorteio")
	m.StartSorteio()
	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "a", "comment": "sorteio"})
	m.handleBridgeEvent("new-chat-message", EventData{"uniqueId": "b", "comment": "sorteio"})
	if _, err := m.DrawSorteio(1); err != nil {
		t.Fatalf("draw: %v", err)
	}

	m.ResetSorteio()
	state := m.SorteioSnapshot()
	if len(state.Participants) != 0 || len(state.Winners) != 0 {
		t.Fatalf("reset must clear pool and winners, got %#v", state)
	}
	if !state.Active {
		t.Fatal("reset must not stop collection")
	}
}

func TestSorteioStateEventsCarrySnapshot(t *testing.T) {
	m, events := newSorteioMonitor(t, "sorteio")
	_, _, _ = m.AddSorteioManualParticipant("Maria")

	m.StartSorteio()
	m.StopSorteio()

	states := 0
	var lastActive bool
	for _, ev := range *events {
		if ev.eventType == EventSorteioState {
			states++
			active, _ := ev.data["active"].(bool)
			total, _ := ev.data["total"].(int)
			lastActive = active
			if total != 1 {
				t.Fatalf("expected snapshot total 1, got %v", ev.data["total"])
			}
		}
	}
	if states != 2 || lastActive {
		t.Fatalf("expected 2 state events ending stopped, got %d (last active=%v)", states, lastActive)
	}
}

// Long runs never wedge: drawing must stay under a sane time budget.
func TestSorteioDrawLargePoolPerformance(t *testing.T) {
	m, _ := newSorteioMonitor(t, "sorteio")
	m.mu.Lock()
	for i := 0; i < 2000; i++ {
		name := "user" + string(rune('a'+i%26)) + time.Now().Format("150405.000000000")
		key := normalizeID(name)
		m.sorteioParticipants[key] = SorteioParticipant{UniqueID: name, Nickname: name, JoinedAt: int64(i), key: key}
	}
	m.mu.Unlock()

	start := time.Now()
	if _, err := m.DrawSorteio(25); err != nil {
		t.Fatalf("draw: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("draw too slow: %v", elapsed)
	}
}
