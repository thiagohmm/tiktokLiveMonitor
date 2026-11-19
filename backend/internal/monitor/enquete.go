package monitor

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"
)

// Enquete ao vivo: o operador define um conjunto de palavras-alvo e cada
// comentário do chat vale como voto na palavra comentada. O voto é travado no
// primeiro comentário válido do usuário: quem votou "não" e depois manda "sim"
// continua valendo "não" (o segundo comentário é ignorado, não troca o voto).
// O estado vive apenas em memória neste Monitor (sem histórico no banco, como
// no Sorteio); reiniciar o backend ou reconectar a live zera a enquete.

const (
	// EventEnqueteState anuncia início/encerramento/limpeza com o snapshot.
	EventEnqueteState = "enquete-state"
	// EventEnqueteVote anuncia um voto recém contabilizado (incremental).
	EventEnqueteVote = "enquete-vote"
	// EventEnqueteResult anuncia a apuração final ao encerrar a enquete.
	EventEnqueteResult = "enquete-result"

	// enqueteOptionsMin é o mínimo de palavras para existir enquete.
	enqueteOptionsMin = 2
	// enqueteOptionsMax limita as palavras de uma enquete.
	enqueteOptionsMax = 10
	// enqueteWordMax limita uma palavra-alvo (máx 40 runes).
	enqueteWordMax = 40
	// enqueteQuestionMax limita o título opcional da enquete.
	enqueteQuestionMax = 80
	// enqueteVotersMax limita os votantes contabilizados em uma enquete.
	enqueteVotersMax = 5000
)

// Enquete errors surfaced by the API as-is (mensagens PT-BR para o painel).
var (
	// ErrEnqueteTooFewOptions means fewer than enqueteOptionsMin words were given.
	ErrEnqueteTooFewOptions = fmt.Errorf("a enquete precisa de no mínimo %d palavras", enqueteOptionsMin)
	// ErrEnqueteTooManyOptions means the word list exceeded enqueteOptionsMax.
	ErrEnqueteTooManyOptions = fmt.Errorf("a enquete aceita no máximo %d palavras", enqueteOptionsMax)
	// ErrEnqueteEmptyOption means one of the words was blank.
	ErrEnqueteEmptyOption = errors.New("informe uma palavra na enquete")
	// ErrEnqueteMultiWordOption means an option has more than one word.
	ErrEnqueteMultiWordOption = errors.New("cada opção da enquete deve ser uma única palavra")
	// ErrEnqueteLongOption means an option exceeded enqueteWordMax runes.
	ErrEnqueteLongOption = fmt.Errorf("a palavra da enquete deve ter no máximo %d caracteres", enqueteWordMax)
	// ErrEnqueteDuplicateOption means two options fold to the same word.
	ErrEnqueteDuplicateOption = errors.New("as palavras da enquete não podem se repetir")
	// ErrEnqueteLongQuestion means the optional title exceeded enqueteQuestionMax.
	ErrEnqueteLongQuestion = fmt.Errorf("o título da enquete deve ter no máximo %d caracteres", enqueteQuestionMax)
	// ErrEnqueteActive means a poll is already collecting votes.
	ErrEnqueteActive = errors.New("encerre a enquete atual antes de iniciar outra")
)

// EnqueteVoter is the chat user who cast the first (and only) vote of a user:
// the vote is locked, so a voter never appears twice in the state.
type EnqueteVoter struct {
	UniqueID string `json:"uniqueId"`
	Nickname string `json:"nickname"`
	VotedAt  int64  `json:"votedAt"`
}

// EnqueteOption is one target word with its counted votes and voters.
type EnqueteOption struct {
	Word   string         `json:"word"`
	Votes  int            `json:"votes"`
	Voters []EnqueteVoter `json:"voters"`
}

// EnqueteState is the in-memory snapshot returned by REST/SSE and used in tests.
type EnqueteState struct {
	Active    bool            `json:"active"`
	Question  string          `json:"question"`
	Options   []EnqueteOption `json:"options"`
	Total     int             `json:"total"`
	Winner    string          `json:"winner"`
	Tied      bool            `json:"tied"`
	StartedAt int64           `json:"startedAt"`
	EndedAt   int64           `json:"endedAt"`
}

// enqueteOption is the internal, mutable option (state copy hides the map).
type enqueteOption struct {
	word   string
	voters []EnqueteVoter
}

// normalizeEnqueteWord folds a word for exact comparison: lowercase, without
// accents, punctuation or emoji, with collapsed spaces. "Não!", "nao 🔥" and
// "  NAO  " all fold to "nao", while "eu voto nao" folds to "eu voto nao"
// (and therefore is NOT a vote).
func normalizeEnqueteWord(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range foldText(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// StartEnquete validates the target words and opens the poll, clearing any
// previous votes. words are the operator's spelling (shown in the panel).
func (m *Monitor) StartEnquete(question string, words []string) (EnqueteState, error) {
	options, err := buildEnqueteOptions(words)
	if err != nil {
		return EnqueteState{}, err
	}
	question = strings.TrimSpace(question)
	if runes := len([]rune(question)); runes > enqueteQuestionMax {
		return EnqueteState{}, ErrEnqueteLongQuestion
	}

	m.mu.Lock()
	if m.enqueteActive {
		m.mu.Unlock()
		return EnqueteState{}, ErrEnqueteActive
	}
	m.enqueteActive = true
	m.enqueteQuestion = question
	m.enqueteOptions = options
	m.enqueteIndex = make(map[string]int, len(options))
	for i, option := range options {
		m.enqueteIndex[normalizeEnqueteWord(option.word)] = i
	}
	m.enqueteVoters = make(map[string]struct{}, len(m.enqueteVoters))
	m.enqueteStartedAt = time.Now().UnixMilli()
	m.enqueteEndedAt = 0
	state := m.enqueteSnapshotLocked()
	m.mu.Unlock()

	m.emit(EventEnqueteState, enqueteStateData(state))
	return state, nil
}

// buildEnqueteOptions validates the operator's word list (2..10 unique single
// words, each up to enqueteWordMax runes).
func buildEnqueteOptions(words []string) ([]enqueteOption, error) {
	options := make([]enqueteOption, 0, len(words))
	seen := make(map[string]bool, len(words))
	for _, raw := range words {
		word := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
		if word == "" {
			continue
		}
		if strings.Contains(word, " ") {
			return nil, ErrEnqueteMultiWordOption
		}
		if runes := len([]rune(word)); runes > enqueteWordMax {
			return nil, ErrEnqueteLongOption
		}
		key := normalizeEnqueteWord(word)
		if key == "" {
			return nil, ErrEnqueteEmptyOption
		}
		if seen[key] {
			return nil, ErrEnqueteDuplicateOption
		}
		seen[key] = true
		options = append(options, enqueteOption{word: word})
	}
	if len(options) < enqueteOptionsMin {
		return nil, ErrEnqueteTooFewOptions
	}
	if len(options) > enqueteOptionsMax {
		return nil, ErrEnqueteTooManyOptions
	}
	return options, nil
}

// StopEnquete closes voting and keeps the votes for the final result.
func (m *Monitor) StopEnquete() {
	m.mu.Lock()
	m.enqueteActive = false
	if m.enqueteEndedAt == 0 {
		m.enqueteEndedAt = time.Now().UnixMilli()
	}
	state := m.enqueteSnapshotLocked()
	m.mu.Unlock()

	m.emit(EventEnqueteState, enqueteStateData(state))
	m.emit(EventEnqueteResult, enqueteStateData(state))
}

// ResetEnquete clears options, votes and the final result.
func (m *Monitor) ResetEnquete() {
	m.mu.Lock()
	m.resetEnqueteLocked()
	state := m.enqueteSnapshotLocked()
	m.mu.Unlock()

	m.emit(EventEnqueteState, enqueteStateData(state))
}

// resetEnqueteLocked drops the whole poll state (used by reset and by a new
// connection, which starts a fresh live); caller holds m.mu.
func (m *Monitor) resetEnqueteLocked() {
	m.enqueteActive = false
	m.enqueteQuestion = ""
	m.enqueteOptions = nil
	m.enqueteIndex = make(map[string]int)
	m.enqueteVoters = make(map[string]struct{})
	m.enqueteStartedAt = 0
	m.enqueteEndedAt = 0
}

// EnqueteSnapshot returns a stable copy of the live's poll state.
func (m *Monitor) EnqueteSnapshot() EnqueteState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enqueteSnapshotLocked()
}

// collectEnqueteVote runs inside handleChatMessage (m.mu travado): quando a
// enquete está ativa e o comentário é exatamente uma das palavras-alvo, conta
// o voto do usuário uma única vez — votos seguintes do mesmo usuário são
// ignorados (o primeiro voto vale, não há troca de ideia).
func (m *Monitor) collectEnqueteVote(comment string, user UserInfo) (pendingEmit, bool) {
	if !m.enqueteActive || len(m.enqueteOptions) == 0 {
		return pendingEmit{}, false
	}
	uniqueID := strings.TrimSpace(user.UniqueID)
	if uniqueID == "" {
		return pendingEmit{}, false
	}
	key := normalizeID(uniqueID)
	if _, voted := m.enqueteVoters[key]; voted {
		return pendingEmit{}, false
	}
	index, ok := m.enqueteIndex[normalizeEnqueteWord(comment)]
	if !ok {
		return pendingEmit{}, false
	}
	if len(m.enqueteVoters) >= enqueteVotersMax {
		log.Printf("[Enquete] votantes cheios (%d): voto de %s ignorado", enqueteVotersMax, uniqueID)
		return pendingEmit{}, false
	}
	nickname := strings.TrimSpace(user.Nickname)
	if nickname == "" {
		nickname = uniqueID
	}
	voter := EnqueteVoter{UniqueID: uniqueID, Nickname: nickname, VotedAt: time.Now().UnixMilli()}
	m.enqueteOptions[index].voters = append(m.enqueteOptions[index].voters, voter)
	m.enqueteVoters[key] = struct{}{}

	return pendingEmit{EventEnqueteVote, EventData{
		"uniqueId": voter.UniqueID,
		"nickname": voter.Nickname,
		"word":     m.enqueteOptions[index].word,
		"index":    index,
		"counts":   m.enqueteCountsLocked(),
		"total":    len(m.enqueteVoters),
		"votedAt":  voter.VotedAt,
	}}, true
}

// enqueteSnapshotLocked builds the public snapshot; caller holds m.mu.
func (m *Monitor) enqueteSnapshotLocked() EnqueteState {
	options := make([]EnqueteOption, 0, len(m.enqueteOptions))
	for _, option := range m.enqueteOptions {
		options = append(options, EnqueteOption{
			Word:   option.word,
			Votes:  len(option.voters),
			Voters: append([]EnqueteVoter(nil), option.voters...),
		})
	}
	state := EnqueteState{
		Active:    m.enqueteActive,
		Question:  m.enqueteQuestion,
		Options:   options,
		Total:     len(m.enqueteVoters),
		StartedAt: m.enqueteStartedAt,
		EndedAt:   m.enqueteEndedAt,
	}
	state.Winner, state.Tied = enqueteWinner(options)
	return state
}

// enqueteWinner returns the leading word and whether the lead is a tie. Com
// empate a primeira opção cadastrada é a vencedora (ordem do operador), para
// que o resultado nunca fique indefinido.
func enqueteWinner(options []EnqueteOption) (string, bool) {
	if len(options) == 0 {
		return "", false
	}
	best, tied := 0, false
	for i, option := range options {
		switch {
		case option.Votes > options[best].Votes:
			best, tied = i, false
		case option.Votes == options[best].Votes && option.Votes > 0 && i != best:
			tied = true
		}
	}
	if options[best].Votes == 0 {
		return "", false
	}
	return options[best].Word, tied
}

// enqueteCountsLocked returns votes per option in registration order.
func (m *Monitor) enqueteCountsLocked() []int {
	counts := make([]int, len(m.enqueteOptions))
	for i, option := range m.enqueteOptions {
		counts[i] = len(option.voters)
	}
	return counts
}

// enqueteStateData serializes the snapshot as an SSE payload.
func enqueteStateData(state EnqueteState) EventData {
	if state.Options == nil {
		state.Options = []EnqueteOption{}
	}
	return EventData{
		"active":    state.Active,
		"question":  state.Question,
		"options":   state.Options,
		"total":     state.Total,
		"winner":    state.Winner,
		"tied":      state.Tied,
		"startedAt": state.StartedAt,
		"endedAt":   state.EndedAt,
	}
}
