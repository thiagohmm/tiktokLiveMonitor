package monitor

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strings"
	"time"
)

// Sorteio: coleta de participantes por palavra-chave no chat + entradas
// manuais do operador e sorteio de N vencedores. O estado vive apenas em
// memória neste Monitor (sem histórico no banco nesta versão); vencedores saem
// da caixa após o sorteio — o próximo sorteio usa os suplentes restantes.

const (
	// EventSorteioState anuncia start/stop/reset da coleta com o snapshot.
	EventSorteioState = "sorteio-state"
	// EventSorteioParticipant anuncia um participante recém adicionado.
	EventSorteioParticipant = "sorteio-participant"
	// EventSorteioResult anuncia os vencedores do último sorteio.
	EventSorteioResult = "sorteio-result"

	// sorteioParticipantsMax limita a caixa de participantes de uma live.
	sorteioParticipantsMax = 5000
	// sorteioNameMax limita o texto de uma entrada manual (máx 40 runes).
	sorteioNameMax = 40
)

// Sorteio errors surfaced by the API as-is (mensagens PT-BR para o painel).
var (
	// ErrSorteioInsufficientPool means the pool has fewer participants than winners.
	ErrSorteioInsufficientPool = errors.New("não há participantes suficientes na caixa do sorteio")
	// ErrSorteioFull means the participant bag reached sorteioParticipantsMax.
	ErrSorteioFull = fmt.Errorf("limite de %d participantes do sorteio atingido", sorteioParticipantsMax)
	// ErrSorteioEmptyName means a manual entry had no usable name.
	ErrSorteioEmptyName = errors.New("informe o nome do participante")
)

// SorteioParticipant is one raffle ticket: a chat user (dedupe por uniqueId)
// or a manual entry typed by the operator.
type SorteioParticipant struct {
	UniqueID string `json:"uniqueId"`
	Nickname string `json:"nickname"`
	Manual   bool   `json:"manual"`
	JoinedAt int64  `json:"joinedAt"`
	// key is the in-memory map key (not serialized; copies keep it).
	key string `json:"-"`
}

// SorteioState is the in-memory snapshot returned by REST and used in tests.
type SorteioState struct {
	Active       bool                 `json:"active"`
	Participants []SorteioParticipant `json:"participants"`
	// Winners holds the last draw's winners (empty after reset/stop free).
	Winners []SorteioParticipant `json:"winners"`
}

// StartSorteio turns chat keyword collection on (keeps existing participants).
func (m *Monitor) StartSorteio() {
	m.mu.Lock()
	m.sorteioActive = true
	data := EventData{"active": true, "participants": m.sorteioParticipantsLocked(), "total": len(m.sorteioParticipants)}
	m.mu.Unlock()
	m.emit(EventSorteioState, data)
}

// StopSorteio stops chat collection; participants and last result are kept so
// the operator can still draw among who is already in.
func (m *Monitor) StopSorteio() {
	m.mu.Lock()
	m.sorteioActive = false
	data := EventData{"active": false, "participants": m.sorteioParticipantsLocked(), "total": len(m.sorteioParticipants)}
	m.mu.Unlock()
	m.emit(EventSorteioState, data)
}

// ResetSorteio clears participants and the last result. The active flag stays
// as-is (start/stop is an independent decision).
func (m *Monitor) ResetSorteio() {
	m.mu.Lock()
	m.sorteioParticipants = make(map[string]SorteioParticipant)
	m.sorteioWinners = nil
	data := EventData{"active": m.sorteioActive, "participants": []SorteioParticipant{}, "total": 0}
	m.mu.Unlock()
	m.emit(EventSorteioState, data)
}

// AddSorteioManualParticipant adds an operator-typed entry. Dedupe: the same
// name (normalized) is never added twice.
func (m *Monitor) AddSorteioManualParticipant(name string) (SorteioParticipant, bool, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if name == "" {
		return SorteioParticipant{}, false, ErrSorteioEmptyName
	}
	if runes := len([]rune(name)); runes > sorteioNameMax {
		return SorteioParticipant{}, false, fmt.Errorf("nome do participante deve ter no máximo %d caracteres", sorteioNameMax)
	}
	key := "manual:" + foldText(name)
	m.mu.Lock()
	if existing, ok := m.sorteioParticipants[key]; ok {
		m.mu.Unlock()
		return existing, false, nil
	}
	if len(m.sorteioParticipants) >= sorteioParticipantsMax {
		m.mu.Unlock()
		return SorteioParticipant{}, false, ErrSorteioFull
	}
	participant := SorteioParticipant{
		UniqueID: name,
		Nickname: name,
		Manual:   true,
		JoinedAt: time.Now().UnixMilli(),
		key:      key,
	}
	m.sorteioParticipants[key] = participant
	total := len(m.sorteioParticipants)
	m.mu.Unlock()

	m.emit(EventSorteioParticipant, sorteioParticipantData(participant, total))
	return participant, true, nil
}

// RemoveSorteioParticipant removes by uniqueId (chat user) or by manual name.
func (m *Monitor) RemoveSorteioParticipant(id string) bool {
	id = strings.TrimPrefix(strings.TrimSpace(id), "@")
	key := normalizeID(id)
	m.mu.Lock()
	if _, exists := m.sorteioParticipants[key]; !exists {
		// Entrada manual: a chave usa foldText (AddSorteioManualParticipant).
		manualKey := "manual:" + foldText(id)
		if _, exists := m.sorteioParticipants[manualKey]; !exists {
			m.mu.Unlock()
			return false
		}
		key = manualKey
	}
	delete(m.sorteioParticipants, key)
	data := EventData{"active": m.sorteioActive, "participants": m.sorteioParticipantsLocked(), "total": len(m.sorteioParticipants)}
	m.mu.Unlock()
	m.emit(EventSorteioState, data)
	return true
}

// SorteioSnapshot returns a stable copy of the live's raffle state.
func (m *Monitor) SorteioSnapshot() SorteioState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return SorteioState{
		Active:       m.sorteioActive,
		Participants: m.sorteioParticipantsLocked(),
		Winners:      append([]SorteioParticipant(nil), m.sorteioWinners...),
	}
}

// DrawSorteio picks count distinct winners from the pool with crypto/rand
// (Fisher–Yates), removes them from the pool and records them as the last
// result. The result is announced through EventSorteioResult.
func (m *Monitor) DrawSorteio(count int) ([]SorteioParticipant, error) {
	if count < 1 {
		count = 1
	}
	if count > sorteioParticipantsMax {
		count = sorteioParticipantsMax
	}
	m.mu.Lock()
	pool := make([]SorteioParticipant, 0, len(m.sorteioParticipants))
	for _, p := range m.sorteioParticipants {
		pool = append(pool, p)
	}
	if len(pool) < count {
		total := len(pool)
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %d participante(s) na caixa, %d solicitado(s)", ErrSorteioInsufficientPool, total, count)
	}
	winners := shuffleTake(pool, count)
	for _, w := range winners {
		if w.key != "" {
			delete(m.sorteioParticipants, w.key)
		}
	}
	m.sorteioWinners = winners
	data := EventData{
		"winners":   append([]SorteioParticipant(nil), winners...),
		"total":     count,
		"remaining": len(m.sorteioParticipants),
		"drawnAt":   time.Now().UnixMilli(),
	}
	m.mu.Unlock()

	m.emit(EventSorteioResult, data)
	return winners, nil
}

// collectSorteioParticipant runs inside handleChatMessage (m.mu travado):
// quando a coleta está ativa e o comentário contém a palavra-chave, adiciona
// o usuário à caixa (dedupe) e devolve o pending emit de novo participante.
func (m *Monitor) collectSorteioParticipant(comment string, user UserInfo) (pendingEmit, bool) {
	if !m.sorteioActive {
		return pendingEmit{}, false
	}
	keyword := strings.TrimSpace(m.settings.SorteioKeyword)
	if keyword == "" {
		return pendingEmit{}, false
	}
	if !strings.Contains(foldText(comment), foldText(keyword)) {
		return pendingEmit{}, false
	}
	uniqueID := strings.TrimSpace(user.UniqueID)
	if uniqueID == "" {
		return pendingEmit{}, false
	}
	key := normalizeID(uniqueID)
	if _, exists := m.sorteioParticipants[key]; exists {
		return pendingEmit{}, false
	}
	if len(m.sorteioParticipants) >= sorteioParticipantsMax {
		log.Printf("[Sorteio] caixa cheia (%d): participante %s ignorado", sorteioParticipantsMax, uniqueID)
		return pendingEmit{}, false
	}
	p := SorteioParticipant{
		UniqueID: uniqueID,
		Nickname: strings.TrimSpace(user.Nickname),
		JoinedAt: time.Now().UnixMilli(),
		key:      key,
	}
	if p.Nickname == "" {
		p.Nickname = p.UniqueID
	}
	m.sorteioParticipants[key] = p
	return pendingEmit{EventSorteioParticipant, sorteioParticipantData(p, len(m.sorteioParticipants))}, true
}

// sorteioParticipantsLocked returns the pool ordered by join time and stable
// fallbacks; caller holds m.mu.
func (m *Monitor) sorteioParticipantsLocked() []SorteioParticipant {
	out := make([]SorteioParticipant, 0, len(m.sorteioParticipants))
	for _, p := range m.sorteioParticipants {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].JoinedAt != out[j].JoinedAt {
			return out[i].JoinedAt < out[j].JoinedAt
		}
		return out[i].UniqueID < out[j].UniqueID
	})
	return out
}

func sorteioParticipantData(p SorteioParticipant, total int) EventData {
	return EventData{
		"uniqueId": p.UniqueID,
		"nickname": p.Nickname,
		"manual":   p.Manual,
		"joinedAt": p.JoinedAt,
		"total":    total,
	}
}

// shuffleTake returns n items of src in random order without touching src.
// Partial Fisher–Yates: swaps only the first n positions (O(n) em vez de
// O(len(src))) para segurar m.mu pelo mínimo necessário.
func shuffleTake(src []SorteioParticipant, n int) []SorteioParticipant {
	if n > len(src) {
		n = len(src)
	}
	out := make([]SorteioParticipant, len(src))
	copy(out, src)
	for i := 0; i < n; i++ {
		j := cryptoRandInt(len(out) - i)
		out[i], out[i+j] = out[i+j], out[i]
	}
	return out[:n]
}

// cryptoRandInt returns a uniform int in [0, max) drawn from crypto/rand.
func cryptoRandInt(max int) int {
	if max <= 1 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		log.Printf("[Sorteio] crypto/rand indisponível: %v (seleção enviesada)", err)
		return 0
	}
	return int(n.Int64())
}
