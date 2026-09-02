package monitor

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"
)

type correlationPick struct {
	match      QuestionEntry
	method     string
	confidence string
}

// correlateGiftWithQuestion correlaciona um presente-alvo com as mensagens do
// doador na janela recente do chat, de forma determinística (sem IA):
//
//  1. candidata única do doador -> heurística determinística (caminho rápido);
//  2. múltiplas mensagens do doador (ou menção por apelido) -> heurística
//     determinística escolhe a melhor mensagem do doador (baixa confiança);
//  3. sem correspondência -> nenhum evento é emitido.
func (m *Monitor) correlateGiftWithQuestion(gift GiftPayload) {
	now := time.Now().UnixMilli()
	correlationID := correlationIDFor(gift, now)

	m.mu.Lock()
	m.pruneQuestions(now)
	questions := append([]QuestionEntry(nil), m.questionBuffer...)
	recent := recentChatCandidatesLocked(m.chatBuffer, now)
	m.mu.Unlock()

	candidates := dedupeCandidates(questions, recent, now)
	if len(candidates) == 0 {
		log.Printf("[Correlation] NO_CANDIDATES | gift=%s | giftUser=%s", gift.GiftName, displayUser(gift.UniqueID, gift.Nickname))
		return
	}

	heuristic := chooseQuestionHeuristic(gift, questions, recent)

	// Caminho rápido: exatamente uma mensagem do doador na janela —
	// correlação inequívoca.
	if heuristic != nil && len(sameUserCandidates(gift, candidates)) == 1 {
		logCorrelation("HEURISTIC_MATCH", gift, heuristic)
		m.emitCorrelation(correlationID, gift, heuristic.match, heuristic.method, heuristic.confidence, false)
		m.scheduleForwardReview(correlationID, gift, heuristic)
		return
	}

	if heuristic != nil {
		pick := &correlationPick{
			match:      heuristic.match,
			method:     heuristic.method + "-fallback",
			confidence: "low",
		}
		logCorrelation("HEURISTIC_FALLBACK", gift, pick)
		m.emitCorrelation(correlationID, gift, pick.match, pick.method, pick.confidence, false)
		m.scheduleForwardReview(correlationID, gift, pick)
		return
	}

	log.Printf("[Correlation] NO_MATCH | gift=%s | giftUser=%s", gift.GiftName, displayUser(gift.UniqueID, gift.Nickname))
}

// sameUserCandidates filtra as candidatas enviadas pelo próprio doador do
// presente (por uid; cai para apelido quando não há uid).
func sameUserCandidates(gift GiftPayload, candidates []QuestionEntry) []QuestionEntry {
	giftUID := normalizeID(gift.UniqueID)
	giftNickFold := foldText(gift.Nickname)
	var out []QuestionEntry
	for _, c := range candidates {
		if giftUID != "" {
			if normalizeID(c.UniqueID) == giftUID {
				out = append(out, c)
			}
			continue
		}
		if giftNickFold != "" && foldText(c.Nickname) == giftNickFold {
			out = append(out, c)
		}
	}
	return out
}

func chooseQuestionHeuristic(gift GiftPayload, questions, recent []QuestionEntry) *correlationPick {
	giftUID := normalizeID(gift.UniqueID)
	giftNickFold := foldText(gift.Nickname)
	questionCandidates := reversedEntries(questions)
	recentCandidates := reversedEntries(recent)
	if len(questionCandidates) == 0 && len(recentCandidates) == 0 {
		return nil
	}

	if giftUID != "" {
		for _, q := range questionCandidates {
			if normalizeID(q.UniqueID) == giftUID {
				return &correlationPick{match: q, method: "same-user-question", confidence: "high"}
			}
		}
		for _, msg := range recentCandidates {
			if normalizeID(msg.UniqueID) == giftUID {
				return &correlationPick{match: msg, method: "same-user-recent-message", confidence: "high"}
			}
		}
	}

	if giftNickFold != "" {
		for _, msg := range recentCandidates {
			if strings.Contains(foldText(msg.Nickname), giftNickFold) {
				return &correlationPick{match: msg, method: "same-nickname-recent-message", confidence: "medium"}
			}
		}
		for _, q := range questionCandidates {
			if strings.Contains(foldText(q.Comment), giftNickFold) {
				return &correlationPick{match: q, method: "nickname-mention", confidence: "medium"}
			}
		}
	}
	return nil
}

func (m *Monitor) scheduleForwardReview(correlationID string, gift GiftPayload, base *correlationPick) {
	if base == nil {
		return
	}
	baseMatch := base.match
	baseMethod := base.method
	baseConfidence := base.confidence

	time.AfterFunc(correlationForwardDelay, func() {
		m.mu.Lock()
		forward := getForwardMessages(baseMatch, m.chatBuffer, correlationForwardCount)
		m.mu.Unlock()
		if len(forward) == 0 {
			return
		}

		bestPick := baseMatch
		bestScore := scoreCorrelationCandidate(baseMatch)
		for _, msg := range forward {
			if score := scoreCorrelationCandidate(msg); score > bestScore {
				bestPick = msg
				bestScore = score
			}
		}

		changed := strings.TrimSpace(bestPick.Comment) != strings.TrimSpace(baseMatch.Comment) ||
			normalizeID(bestPick.UniqueID) != normalizeID(baseMatch.UniqueID)
		if !changed || bestScore < scoreCorrelationCandidate(baseMatch)+0.5 {
			return
		}

		method := fmt.Sprintf("%s+forward-%d", baseMethod, correlationForwardCount)
		m.emitCorrelation(correlationID, gift, bestPick, method, baseConfidence, true)
	})
}

func (m *Monitor) emitCorrelation(correlationID string, gift GiftPayload, pick QuestionEntry, method, confidence string, replacement bool) {
	m.emit(EventGiftQuestionCorr, EventData{
		"correlationId":    correlationID,
		"giftName":         gift.GiftName,
		"giftUserId":       gift.UniqueID,
		"giftNickname":     gift.Nickname,
		"questionUserId":   pick.UniqueID,
		"questionNickname": pick.Nickname,
		"question":         pick.Comment,
		"isFollower":       pick.IsFollower,
		"method":           method,
		"confidence":       confidence,
		"replacement":      replacement,
		"timestamp":        time.Now().UnixMilli(),
	})
}

func recentChatCandidatesLocked(chat []ChatMessage, now int64) []QuestionEntry {
	cutoff := now - questionCorrelationWindow.Milliseconds()
	out := make([]QuestionEntry, 0, 40)
	for _, msg := range chat {
		if msg.Timestamp < cutoff {
			continue
		}
		out = append(out, QuestionEntry(msg))
	}
	if len(out) > 40 {
		out = out[len(out)-40:]
	}
	return out
}

func getForwardMessages(base QuestionEntry, chat []ChatMessage, limit int) []QuestionEntry {
	if base.Timestamp == 0 {
		return nil
	}
	var forward []ChatMessage
	for _, msg := range chat {
		if msg.Timestamp > base.Timestamp {
			forward = append(forward, msg)
		}
	}
	if len(forward) == 0 {
		return nil
	}

	var sameAuthor []ChatMessage
	for _, msg := range forward {
		if sameMessageIdentity(base, QuestionEntry{UniqueID: msg.UniqueID, Nickname: msg.Nickname}) {
			sameAuthor = append(sameAuthor, msg)
		}
	}
	source := forward
	if len(sameAuthor) > 0 {
		source = sameAuthor
	}
	if limit < 1 {
		limit = 1
	}
	if len(source) > limit {
		source = source[:limit]
	}
	out := make([]QuestionEntry, 0, len(source))
	for _, msg := range source {
		out = append(out, QuestionEntry(msg))
	}
	return out
}

func scoreCorrelationCandidate(candidate QuestionEntry) float64 {
	text := strings.TrimSpace(candidate.Comment)
	if text == "" {
		return 0
	}
	score := 0.0
	if looksLikeQuestion(text) {
		score += 3
	}
	if strings.ContainsAny(text, "?¿") {
		score += 1
	}
	folded := foldText(text)
	cue := regexp.MustCompile(`\b(pq|pk|por\s+que|porque|como|quando|onde|aonde|quem|qual|duvida|tem\s+como|da\s+pra|d[aá]\s+pra)\b`)
	if cue.MatchString(folded) {
		score += 1
	}
	if len(text) >= 8 && len(text) <= 220 {
		score += 0.5
	}
	return score
}

func sameMessageIdentity(a, b QuestionEntry) bool {
	aUID := normalizeID(a.UniqueID)
	bUID := normalizeID(b.UniqueID)
	if aUID != "" && bUID != "" {
		return aUID == bUID
	}
	aNick := foldText(a.Nickname)
	bNick := foldText(b.Nickname)
	return aNick != "" && aNick == bNick
}

func reversedEntries(in []QuestionEntry) []QuestionEntry {
	out := make([]QuestionEntry, len(in))
	for i, q := range in {
		out[len(in)-1-i] = q
	}
	return out
}

func dedupeCandidates(questions, recent []QuestionEntry, now int64) []QuestionEntry {
	cutoff := now - questionCorrelationWindow.Milliseconds()
	seen := map[string]bool{}
	out := make([]QuestionEntry, 0, len(questions)+len(recent))
	appendUnique := func(list []QuestionEntry) {
		for _, q := range list {
			if q.Timestamp < cutoff {
				continue
			}
			key := normalizeID(q.UniqueID) + "|" + strings.TrimSpace(q.Comment) + "|" + fmt.Sprintf("%d", q.Timestamp)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, q)
		}
	}
	appendUnique(questions)
	appendUnique(recent)
	// Ordem cronológica (mais antigo -> mais recente).
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Timestamp < out[j].Timestamp
	})
	if len(out) > 8 {
		out = out[len(out)-8:]
	}
	return out
}

func correlationIDFor(gift GiftPayload, now int64) string {
	user := normalizeID(gift.UniqueID)
	if user == "" {
		user = foldText(gift.Nickname)
	}
	if user == "" {
		user = "anon"
	}
	return fmt.Sprintf("corr-%s-%d-%d", user, now, now%100000)
}

func displayUser(uniqueID, nickname string) string {
	if nickname != "" {
		return nickname
	}
	if uniqueID != "" {
		return uniqueID
	}
	return "-"
}

func logCorrelation(event string, gift GiftPayload, pick *correlationPick) {
	question := ""
	method := ""
	confidence := ""
	qUser := "-"
	if pick != nil {
		question = strings.Join(strings.Fields(pick.match.Comment), " ")
		if len(question) > 120 {
			question = question[:120]
		}
		method = pick.method
		confidence = pick.confidence
		qUser = displayUser(pick.match.UniqueID, pick.match.Nickname)
	}
	log.Printf("[Correlation] %s | gift=%s | giftUser=%s | method=%s | confidence=%s | questionUser=%s | question=%q",
		event, gift.GiftName, displayUser(gift.UniqueID, gift.Nickname), method, confidence, qUser, question)
}
