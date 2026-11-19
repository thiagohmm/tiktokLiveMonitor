package monitor

import (
	"errors"
	"strings"
	"testing"
)

func newEnqueteMonitor(t *testing.T) (*Monitor, *[]capturedEvent) {
	t.Helper()
	m, _ := New()
	events := &[]capturedEvent{}
	m.OnEvent(func(eventType string, data EventData) {
		*events = append(*events, capturedEvent{eventType, data})
	})
	return m, events
}

// comentar envia um comentário pelo caminho real do bridge (mutex + collects).
func comentar(m *Monitor, uniqueID, nickname, comment string) {
	m.handleBridgeEvent("new-chat-message", EventData{
		"uniqueId": uniqueID,
		"nickname": nickname,
		"comment":  comment,
	})
}

func startEnquete(t *testing.T, m *Monitor, question string, words ...string) EnqueteState {
	t.Helper()
	state, err := m.StartEnquete(question, words)
	if err != nil {
		t.Fatalf("start enquete %v: %v", words, err)
	}
	return state
}

func countVoteEvents(events []capturedEvent) int {
	total := 0
	for _, ev := range events {
		if ev.eventType == EventEnqueteVote {
			total++
		}
	}
	return total
}

func optionVotes(state EnqueteState, word string) int {
	for _, option := range state.Options {
		if option.Word == word {
			return option.Votes
		}
	}
	return -1
}

func TestEnqueteRequiresTwoWords(t *testing.T) {
	m, _ := newEnqueteMonitor(t)

	cases := []struct {
		name  string
		words []string
		want  error
	}{
		{"nenhuma", nil, ErrEnqueteTooFewOptions},
		{"uma", []string{"sim"}, ErrEnqueteTooFewOptions},
		{"vazia é ignorada", []string{"sim", "   "}, ErrEnqueteTooFewOptions},
		{"duplicada foldada", []string{"sim", "Sim", "não"}, ErrEnqueteDuplicateOption},
		{"duplicada com acento", []string{"não", "nao"}, ErrEnqueteDuplicateOption},
		{"frase", []string{"sim", "com certeza"}, ErrEnqueteMultiWordOption},
		{"longa", []string{"sim", "abcdefghijklmnopqrstuvwxyzabcdefghijklmno"}, ErrEnqueteLongOption},
		{"título longo", nil, ErrEnqueteLongQuestion},
		{"acima do limite", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}, ErrEnqueteTooManyOptions},
	}
	longQuestion := strings.Repeat("a", enqueteQuestionMax+1)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			question := ""
			if errors.Is(tc.want, ErrEnqueteLongQuestion) {
				question = longQuestion
				tc.words = []string{"sim", "não"}
			}
			if _, err := m.StartEnquete(question, tc.words); !errors.Is(err, tc.want) {
				t.Fatalf("words=%v: expected %v, got %v", tc.words, tc.want, err)
			}
			if m.EnqueteSnapshot().Active {
				t.Fatal("enquete inválida não pode ficar ativa")
			}
		})
	}

	// Dois estados válidos: enquete aberta com as palavras do operador.
	state := startEnquete(t, m, "Vamos jogar?", "Sim", "Não")
	if !state.Active || state.Question != "Vamos jogar?" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if len(state.Options) != 2 || state.Options[0].Word != "Sim" || state.Options[1].Word != "Não" {
		t.Fatalf("unexpected options: %#v", state.Options)
	}
}

// O voto exige o comentário igual à palavra: caixa/acento/pontuação/emoji são
// tolerados, frase com a palavra dentro não vale.
func TestEnqueteVoteMatchesWholeComment(t *testing.T) {
	m, events := newEnqueteMonitor(t)
	startEnquete(t, m, "", "sim", "não")

	comentar(m, "u1", "Ana", "não")
	comentar(m, "u2", "Bia", "Não!")
	comentar(m, "u3", "Caio", "nao 🔥")
	comentar(m, "u4", "Dan", "   NAO   ")
	comentar(m, "u5", "Eva", "Sim")
	comentar(m, "u6", "Fabio", "eu voto não") // frase: não vota
	comentar(m, "u7", "Gil", "não sei ainda") // frase: não vota
	comentar(m, "u8", "Hugo", "naosei")       // outra palavra: não vota

	state := m.EnqueteSnapshot()
	if got := optionVotes(state, "não"); got != 4 {
		t.Fatalf("expected 4 votes on 'não', got %d (%#v)", got, state)
	}
	if got := optionVotes(state, "sim"); got != 1 {
		t.Fatalf("expected 1 vote on 'sim', got %d (%#v)", got, state)
	}
	if state.Total != 5 {
		t.Fatalf("expected 5 counted votes, got %d", state.Total)
	}
	if state.Winner != "não" || state.Tied {
		t.Fatalf("expected winner 'não' without tie, got %#v", state)
	}
	if got := countVoteEvents(*events); got != 5 {
		t.Fatalf("expected 5 enquete-vote events, got %d", got)
	}
}

// O primeiro voto vale: quem votou "não" e depois manda "sim" não muda de ideia.
func TestEnqueteLocksFirstVote(t *testing.T) {
	m, events := newEnqueteMonitor(t)
	startEnquete(t, m, "", "sim", "não")

	comentar(m, "joao", "João", "não")
	comentar(m, "joao", "João", "sim")
	comentar(m, "JOAO", "João", "sim") // mesmo usuário com outra caixa
	comentar(m, "maria", "Maria", "sim")

	state := m.EnqueteSnapshot()
	if got := optionVotes(state, "não"); got != 1 {
		t.Fatalf("expected João's vote to stay on 'não', got %d", got)
	}
	if got := optionVotes(state, "sim"); got != 1 {
		t.Fatalf("expected only Maria's vote on 'sim', got %d", got)
	}
	if state.Total != 2 {
		t.Fatalf("expected 2 voters, got %d", state.Total)
	}
	if got := countVoteEvents(*events); got != 2 {
		t.Fatalf("expected 2 enquete-vote events (one per user), got %d", got)
	}
	voter := state.Options[0].Voters
	if len(voter) != 1 || voter[0].Nickname != "Maria" {
		t.Fatalf("unexpected voters on 'sim': %#v", voter)
	}
	if voter := state.Options[1].Voters; len(voter) != 1 || voter[0].UniqueID != "joao" || voter[0].Nickname != "João" {
		t.Fatalf("unexpected voters on 'não': %#v", voter)
	}
}

func TestEnqueteVoteEventCarriesCounts(t *testing.T) {
	m, events := newEnqueteMonitor(t)
	startEnquete(t, m, "", "sim", "não")

	comentar(m, "u1", "Ana", "sim")
	comentar(m, "u2", "Bia", "sim")

	var last EventData
	for _, ev := range *events {
		if ev.eventType == EventEnqueteVote {
			last = ev.data
		}
	}
	if last == nil {
		t.Fatal("expected an enquete-vote event")
	}
	if last["word"] != "sim" || last["nickname"] != "Bia" || last["uniqueId"] != "u2" {
		t.Fatalf("unexpected vote payload: %#v", last)
	}
	if counts, ok := last["counts"].([]int); !ok || len(counts) != 2 || counts[0] != 2 || counts[1] != 0 {
		t.Fatalf("expected counts [2 0], got %#v", last["counts"])
	}
	if total, _ := last["total"].(int); total != 2 {
		t.Fatalf("expected total 2, got %#v", last["total"])
	}
}

func TestEnqueteInactiveIgnoresVotesAndStopKeepsResult(t *testing.T) {
	m, _ := newEnqueteMonitor(t)

	// Sem enquete iniciada não existe palavra-alvo: nada é contado.
	comentar(m, "u1", "Ana", "sim")
	if got := m.EnqueteSnapshot().Total; got != 0 {
		t.Fatalf("expected no votes without an open poll, got %d", got)
	}

	startEnquete(t, m, "", "sim", "não")
	comentar(m, "u1", "Ana", "sim")

	m.StopEnquete()
	comentar(m, "u2", "Bia", "sim")

	state := m.EnqueteSnapshot()
	if state.Active {
		t.Fatal("enquete deve estar encerrada")
	}
	if state.Total != 1 || optionVotes(state, "sim") != 1 {
		t.Fatalf("encerrada não deve aceitar votos: %#v", state)
	}
	if state.Winner != "sim" {
		t.Fatalf("expected winner 'sim', got %#v", state)
	}
	if state.EndedAt == 0 {
		t.Fatal("expected endedAt set on stop")
	}
}

func TestEnqueteWinnerTieAndReset(t *testing.T) {
	m, _ := newEnqueteMonitor(t)
	startEnquete(t, m, "", "sim", "não")

	comentar(m, "u1", "Ana", "sim")
	comentar(m, "u2", "Bia", "não")

	state := m.EnqueteSnapshot()
	if !state.Tied || state.Winner != "sim" {
		t.Fatalf("expected tie resolved by the first registered word, got %#v", state)
	}

	// Reset limpa opções, votos e resultado.
	m.ResetEnquete()
	state = m.EnqueteSnapshot()
	if state.Active || state.Total != 0 || len(state.Options) != 0 || state.Winner != "" || state.Tied {
		t.Fatalf("reset deve zerar a enquete, got %#v", state)
	}

	// Depois do reset a enquete pode ser aberta de novo, com outras palavras.
	startEnquete(t, m, "", "azul", "verde")
	comentar(m, "u1", "Ana", "não")
	comentar(m, "u1", "Ana", "verde")
	if got := optionVotes(m.EnqueteSnapshot(), "verde"); got != 1 {
		t.Fatalf("expected 1 vote on 'verde' after reset, got %d", got)
	}
}

func TestEnqueteRejectsStartWhileActive(t *testing.T) {
	m, _ := newEnqueteMonitor(t)
	startEnquete(t, m, "", "sim", "não")
	comentar(m, "u1", "Ana", "sim")

	if _, err := m.StartEnquete("", []string{"azul", "verde"}); !errors.Is(err, ErrEnqueteActive) {
		t.Fatalf("expected ErrEnqueteActive, got %v", err)
	}
	if got := optionVotes(m.EnqueteSnapshot(), "sim"); got != 1 {
		t.Fatalf("start rejeitado não pode zerar os votos, got %d", got)
	}
}

// A enquete é zerada quando a live reconecta (novo StartMonitoring).
func TestEnqueteResetOnNewConnection(t *testing.T) {
	m, _ := newEnqueteMonitor(t)
	startEnquete(t, m, "", "sim", "não")
	comentar(m, "u1", "Ana", "sim")

	m.resetEnqueteLocked()

	if state := m.EnqueteSnapshot(); state.Active || state.Total != 0 || len(state.Options) != 0 {
		t.Fatalf("nova conexão deve zerar a enquete, got %#v", state)
	}
}

func TestEnqueteVoterLimitAndNicknameFallback(t *testing.T) {
	m, _ := newEnqueteMonitor(t)
	startEnquete(t, m, "", "sim", "não")

	comentar(m, "sem-nick", "", "sim")
	state := m.EnqueteSnapshot()
	if len(state.Options[0].Voters) != 1 || state.Options[0].Voters[0].Nickname != "sem-nick" {
		t.Fatalf("expected uniqueId as nickname fallback, got %#v", state.Options[0].Voters)
	}

	// Limite de votantes: o voto excedente é ignorado (estado continua íntegro).
	m.mu.Lock()
	for i := 0; i < enqueteVotersMax; i++ {
		m.enqueteVoters[normalizeID("cheio-"+itoaTest(i))] = struct{}{}
	}
	m.mu.Unlock()
	comentar(m, "excedente", "Excedente", "não")
	if got := optionVotes(m.EnqueteSnapshot(), "não"); got != 0 {
		t.Fatalf("voto acima do limite deve ser ignorado, got %d", got)
	}
}

// itoaTest evita importar strconv só por causa do teste do limite.
func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
