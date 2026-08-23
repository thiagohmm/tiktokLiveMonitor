// Package moderation provides the message analysis pipeline using LLM + rule-based classifiers.
package moderation

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

// Category labels for display.
var CategoryLabels = map[string]string{
	"PROSELITISMO": "Proselitismo Cristão",
	"SPAM":         "Spam / propaganda (IA)",
	"GOLPE":        "Possível golpe ou fraude (IA)",
	"ODIO":         "Ódio / insulto grave (IA)",
	"PERGUNTA":     "Pergunta / Dúvida (IA)",
	"OUTRO":        "Conteúdo impróprio (IA)",
}

// StartupStatus tracks moderation warmup state.
type StartupStatus struct {
	Ready         bool   `json:"ready"`
	FeedbackCount int    `json:"feedbackCount"`
	WarmedAt      string `json:"warmedAt"`
	LastError     string `json:"lastError"`
}

// AnalysisResult is the output of message moderation.
type AnalysisResult struct {
	Flagged  bool   `json:"flagged"`
	Category string `json:"category"`
	Reason   string `json:"reason"`
}

const (
	aiCacheMax         = 150
	aiCooldownMs       = 30_000
	auditReclassifyEnv = "MODERATION_AUDIT_RECLASSIFY"
	recentChatLimit    = 14
)

var (
	auditEnabled bool
)

func init() {
	auditEnabled = isTruthy(strings.ToLower(strings.TrimSpace(
		coalesceEnv(auditReclassifyEnv, ""),
	)))
}

// Engine handles message moderation.
type Engine struct {
	mu            sync.Mutex
	aiManager     *ai.Manager
	repo          model.Repository
	cache         map[string]AnalysisResult
	cooldownUntil int64
	warmupStatus  StartupStatus
	warmupFlight  *sync.Mutex
	allowlist     map[string]struct{}
}

// NewEngine creates a moderation engine.
func NewEngine(aiMgr *ai.Manager, repo model.Repository) *Engine {
	return &Engine{
		aiManager:    aiMgr,
		repo:         repo,
		cache:        make(map[string]AnalysisResult),
		warmupFlight: &sync.Mutex{},
		allowlist:    make(map[string]struct{}),
	}
}

// refreshAllowlist reloads normalized false-positive comments from the repository.
func (e *Engine) refreshAllowlist() {
	if e.repo == nil {
		return
	}
	comments, err := e.repo.GetFalsePositiveComments(500)
	if err != nil {
		log.Printf("[Moderation] Falha ao carregar allowlist: %v", err)
		return
	}
	allow := make(map[string]struct{}, len(comments))
	for _, c := range comments {
		folded := foldText(strings.ToLower(strings.TrimSpace(c)))
		if folded == "" {
			continue
		}
		allow[folded] = struct{}{}
	}
	e.mu.Lock()
	e.allowlist = allow
	e.mu.Unlock()
}

// GetStartupStatus returns the current warmup status.
func (e *Engine) GetStartupStatus() StartupStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.warmupStatus
}

// ClearCache resets the AI cache and cooldown.
func (e *Engine) ClearCache() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache = make(map[string]AnalysisResult)
	e.cooldownUntil = 0
}

// WarmupLearning warms up the moderation pipeline with few-shot examples.
func (e *Engine) WarmupLearning(ctx context.Context, touchLLM, force bool) (StartupStatus, error) {
	if !force {
		e.warmupFlight.Lock()
		defer e.warmupFlight.Unlock()
	}

	e.mu.Lock()
	if e.warmupStatus.Ready && !force {
		e.mu.Unlock()
		return e.warmupStatus, nil
	}
	e.mu.Unlock()

	prompt, feedbackCount, err := buildPromptContext(ctx, e.repo, 24)
	if err != nil {
		e.mu.Lock()
		e.warmupStatus = StartupStatus{
			Ready:     false,
			LastError: err.Error(),
		}
		e.mu.Unlock()
		return e.warmupStatus, fmt.Errorf("build prompt: %w", err)
	}

	if touchLLM {
		req := ai.CompletionRequest{
			SystemContent: prompt,
			UserContent:   `Contexto recente (mensagens anteriores na live): (nenhuma mensagem anterior no buffer)\n\nAutor do comentário: "system"\nTexto para analisar (ignore menções @nome no início): "mensagem de aquecimento"`,
			MaxTokens:     8,
		}
		if _, err := e.aiManager.Complete(ctx, req); err != nil {
			log.Printf("[Moderation] Warmup LLM falhou: %v", err)
		}
	}

	e.refreshAllowlist()

	e.mu.Lock()
	e.warmupStatus = StartupStatus{
		Ready:         true,
		FeedbackCount: feedbackCount,
		WarmedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	e.mu.Unlock()

	return e.warmupStatus, nil
}

// AnalyzeMessage classifies a chat message using AI + rules.
func (e *Engine) AnalyzeMessage(ctx context.Context, comment, uniqueID, nickname string, chatBuf []monitor.ChatMessage, liveName string) (AnalysisResult, error) {
	commentLower := strings.TrimSpace(strings.ToLower(comment))
	folded := foldText(commentLower)

	e.mu.Lock()
	if _, ok := e.allowlist[folded]; ok {
		e.mu.Unlock()
		log.Printf("[AI] ✅ liberado por allowlist: %q", truncate(comment, 50))
		return AnalysisResult{Flagged: false, Category: "OK"}, nil
	}

	// Cooldown check.
	if time.Now().UnixMilli() < e.cooldownUntil {
		e.mu.Unlock()
		return AnalysisResult{Flagged: false}, nil
	}

	// Cache check.
	cacheKey := truncate(folded, 500)
	if cached, ok := e.cache[cacheKey]; ok {
		e.mu.Unlock()
		return cached, nil
	}
	e.mu.Unlock()

	// Build prompt and call AI.
	prompt, _, err := buildPromptContext(ctx, e.repo, 24)
	if err != nil {
		return AnalysisResult{Flagged: false}, fmt.Errorf("build prompt: %w", err)
	}

	contextBlock := buildRecentChatBlock(chatBuf)
	userPrompt := fmt.Sprintf(
		"Contexto recente (mensagens anteriores na live):\n%s\n\nAutor do comentário: %s\nTexto para analisar (ignore menções @nome no início): %s",
		contextBlock,
		jsonString(coalesceStr(nickname, uniqueID, "")),
		jsonString(comment),
	)

	req := ai.CompletionRequest{
		SystemContent: prompt,
		UserContent:   userPrompt,
		MaxTokens:     48,
	}

	raw, err := e.aiManager.Complete(ctx, req)
	if err != nil {
		e.mu.Lock()
		e.cooldownUntil = time.Now().UnixMilli() + aiCooldownMs
		e.mu.Unlock()
		log.Printf("[Moderation] IA pausada (falha): %v", err)
		return AnalysisResult{Flagged: false}, nil
	}

	result := parseAIResponse(raw, comment, liveName, uniqueID, nickname)

	// Store in cache.
	e.mu.Lock()
	e.cache[cacheKey] = result
	if len(e.cache) > aiCacheMax {
		for k := range e.cache {
			delete(e.cache, k)
			break
		}
	}
	e.mu.Unlock()

	if result.Flagged {
		log.Printf("[AI] ⚠️ CONTEÚDO FLAGADO: [%s] - %q", result.Category, comment)
	} else {
		preview := truncate(comment, 50)
		log.Printf("[AI] ✅ Conteúdo liberado: %q", preview)
	}

	// Log to database asynchronously.
	go func() {
		if err := e.repo.LogAnomaly(liveName, comment, result.Flagged, result.Category, uniqueID); err != nil {
			log.Printf("[Database] Erro ao logar anomalia: %v", err)
		}
	}()

	return result, nil
}

// --- AI Response Parsing ---

func parseAIResponse(raw, originalComment, liveName, uniqueID, nickname string) AnalysisResult {
	key := normalizeModerationKeyword(raw)

	if key == "" || strings.HasPrefix(key, "nao") {
		return AnalysisResult{Flagged: false, Category: "OK"}
	}

	prefixMap := map[string]string{
		"sim_odio":         "ODIO",
		"sim_proselitismo": "PROSELITISMO",
		"sim_spam":         "SPAM",
		"sim_golpe":        "GOLPE",
		"sim_pergunta":     "PERGUNTA",
		"sim_outro":        "OUTRO",
	}

	var category string
	for prefix, cat := range prefixMap {
		if key == prefix || strings.HasPrefix(key, prefix) {
			category = cat
			break
		}
	}

	if category == "" {
		compact := strings.TrimSpace(strings.ToLower(raw))
		if strings.HasPrefix(compact, "sim") {
			category = "PROSELITISMO"
		} else {
			return AnalysisResult{Flagged: false, Category: "OK"}
		}
	}

	flagged := category != "PERGUNTA"

	// Rule-based post-processing.
	result := AnalysisResult{Flagged: flagged, Category: category}

	// Reclassify ODIO to PERGUNTA for questions without personal attacks.
	if category == "ODIO" && looksQuestion(originalComment) && !hasClearPersonalAttackSignal(originalComment) {
		logAudit("reclassified_odio_to_pergunta", liveName, uniqueID, nickname, raw, category, "PERGUNTA", originalComment)
		result.Category = "PERGUNTA"
		result.Flagged = false
	}

	// Reclassify ODIO for affective/romantic language.
	if category == "ODIO" && flagged && looksAffectiveOrRomantic(originalComment) {
		logAudit("reclassified_odio_to_nao_affective", liveName, uniqueID, nickname, raw, category, "NAO", originalComment)
		result.Category = "OK"
		result.Flagged = false
	}

	// Reclassify ODIO without clear personal attack signals.
	if category == "ODIO" && flagged && !hasClearPersonalAttackSignal(originalComment) {
		logAudit("reclassified_odio_to_nao_no_signal", liveName, uniqueID, nickname, raw, category, "NAO", originalComment)
		result.Category = "OK"
		result.Flagged = false
	}

	if result.Flagged {
		result.Reason = getCategoryLabel(result.Category)
	}

	return result
}

// --- Text Processing Helpers ---

func foldText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ç", "c")
	// Strip combining marks.
	var b strings.Builder
	for _, r := range s {
		if r >= 0x0300 && r <= 0x036F {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func normalizeModerationKeyword(raw string) string {
	folded := foldText(raw)
	// Replace whitespace with underscore and remove non-alphanumeric.
	folded = regexp.MustCompile(`\s+`).ReplaceAllString(folded, "_")
	folded = regexp.MustCompile(`[^a-z0-9_]`).ReplaceAllString(folded, "")
	return folded
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// --- Rule-based Classifiers ---

func looksQuestion(comment string) bool {
	raw := strings.TrimSpace(comment)
	if raw == "" {
		return false
	}
	if strings.ContainsAny(raw, "?¿") {
		return true
	}
	folded := foldText(raw)
	startsLike := regexp.MustCompile(
		`^(pq|pk|por\s+que|porque|como|quando|onde|aonde|quem|qual|quais|q\b|sera\s+que|pode|poderia|tem\s+como|da\s+pra|d[aá]\s+pra|isso\s+e|isso\s+eh|v[oô]ce\s+sabe|alguem\s+sabe|algm\s+sabe|me\s+tira\s+uma\s+duvida|duvida\b|duvida:|duvida\s*[-:])`,
	)
	if startsLike.MatchString(folded) {
		return true
	}
	containsCue := regexp.MustCompile(
		`\b(pq|pk|por\s+que|como\s+assim|quem\s+sabe|alguem\s+sabe|algm\s+sabe|tem\s+como|da\s+pra|d[aá]\s+pra|sera\s+que|qual\s+o|qual\s+a)\b`,
	)
	return containsCue.MatchString(folded)
}

func passesChristianProselytizingAiGate(commentLower string) bool {
	t := foldText(commentLower)
	jc := regexp.MustCompile(`\b(jesus|cristo|jeova)\b`).MatchString(t)
	afroCtx := regexp.MustCompile(`\b(candombl|umbanda|macumba|orixa[s]?|feitico[s]?|terreiro|og[aã]|vodum)\b`).MatchString(t)
	if jc && afroCtx {
		return true
	}
	jesusSalva := regexp.MustCompile(`\b(jesus|cristo|deus)\s+salva\b`).MatchString(t)
	soJesus := regexp.MustCompile(`\bso\s+(jesus|cristo|deus)(\s+salva)?\b`).MatchString(t)
	if jesusSalva || soJesus {
		return true
	}
	salvacao := regexp.MustCompile(`\b(jesus|cristo|deus)\b`).MatchString(t) &&
		regexp.MustCompile(`\b(salva[cç]ao|salva)\b`).MatchString(t)
	if salvacao {
		return true
	}
	tension := regexp.MustCompile(`\b(converter|salvacao|entregar|arrep|pecado|cruz|inferno|pregac|culto|pregador)\b`).MatchString(t)
	if jc && tension {
		return true
	}
	deusTension := regexp.MustCompile(`\bdeus\b`).MatchString(t) &&
		regexp.MustCompile(`\b(converter|salvacao|inferno|pecado|cruz|arrep)\b`).MatchString(t)
	if deusTension {
		return true
	}
	if regexp.MustCompile(`\bigreja\b`).MatchString(t) || regexp.MustCompile(`\bpastor\b`).MatchString(t) {
		return true
	}
	return false
}

func hasExternalShortlinkOrMessenger(rawComment string) bool {
	return regexp.MustCompile(`(?i)bit\.ly\/|tinyurl\.com\/|cutt\.ly\/|wa\.me\/|t\.me\/|telegram\.me\/`).MatchString(rawComment)
}

func hasNonTiktokHttpLink(rawComment string) bool {
	urlRe := regexp.MustCompile(`(?i)https?:\/\/[^\s]+|www\.[^\s]+`)
	if !urlRe.MatchString(rawComment) {
		return false
	}
	tiktokRe := regexp.MustCompile(`(?i)tiktok\.com|vm\.tiktok\.com|vt\.tiktok\.com`)
	urls := urlRe.FindAllString(rawComment, -1)
	for _, u := range urls {
		if !tiktokRe.MatchString(u) {
			return true
		}
	}
	return false
}

func passesSpamScamAiGate(rawComment, folded string) bool {
	t := coalesceStr(folded, foldText(rawComment))
	if hasExternalShortlinkOrMessenger(rawComment) || hasNonTiktokHttpLink(rawComment) {
		return true
	}
	patterns := []string{
		`\b(pix\s+qrcode|pix\s+copia|mande\s+pix|clica\s+no\s+link|link\s+na\s+bio|link\s+do\s+perfil)\b`,
		`\b(ganhe\s+(dinheiro|gratis)|dinheiro\s+facil)\b`,
	}
	for _, p := range patterns {
		if regexp.MustCompile(p).MatchString(t) {
			return true
		}
	}
	if regexp.MustCompile(`\bcurso\s+gratis\b`).MatchString(t) &&
		regexp.MustCompile(`(?i)https?:\/\/`).MatchString(rawComment) {
		return true
	}
	return false
}

func passesRegionalSlurAiGate(folded string) bool {
	return regexp.MustCompile(`\b(testud[oa]|marmoteir[oa]|enganad[oa])\b`).MatchString(folded)
}

func passesPersonalAttackAiGate(folded string) bool {
	if passesRegionalSlurAiGate(folded) {
		return true
	}
	directed := regexp.MustCompile(`\b(voc[eê]|voce\b|\bvc\b|\bce\b|tu\s+t[eá]|pra\s+voce\b|pra\s+voc[eê])\b`).MatchString(folded)
	insultCore := regexp.MustCompile(`\b(idiota|imbecil|burr[oa]|estupid[oa]|nojent[oa]|noj[o]|lixo|palha[cç][oa]|ridicul[oa]|inutil|fracassad[oa])\b`).MatchString(folded)
	strongSlur := regexp.MustCompile(
		`\b(filho\s+da\s+puta|filho\s+de\s+puta|fdp\b|vsf\b|vtnc\b|vai\s+(tomar\s+no\s+cu|pro\s+inferno|a\s+merda)|se\s+fod(e|eu)|pau\s+no\s+cu|cuz[aã]o|escrot[oa])\b`,
	).MatchString(folded)
	threatShut := regexp.MustCompile(
		`\b(morre\b|apaga(\s+a\s+live)?|some(\s+daqui)?|cal[aá]\s+(a\s+)?boca|para\s+de\s+falar|cala\s+boca|te\s+arrodo|te\s+quebro)\b`,
	).MatchString(folded)
	familyAttack := regexp.MustCompile(`\b(sua\s+m[aã]e|teu\s+pai|tua\s+familia)\b`).MatchString(folded) &&
		regexp.MustCompile(`\b(puta|viad[o]|burr[o]?)\b`).MatchString(folded)

	if strongSlur || threatShut || familyAttack {
		return true
	}
	if directed && insultCore {
		return true
	}
	return false
}

func hasClearPersonalAttackSignal(comment string) bool {
	folded := foldText(comment)
	if passesPersonalAttackAiGate(folded) {
		return true
	}
	return regexp.MustCompile(
		`\b(vc|voce|voces|tu|ce|c\b)\b[\s\S]{0,20}\b(e\s+)?(burro|idiota|imbecil|retardad[oa]|ridicul[oa]|otari[oa]|troux[ae]|lixo)\b`,
	).MatchString(folded)
}

func looksAffectiveOrRomantic(comment string) bool {
	t := foldText(comment)
	patterns := []string{
		`\b(gosta\s+d[eio]|gostar\s+d[eio]|gostou\s+d[eio])\b`,
		`\b(vai\s+atras|vai\s+atr[aá]s|foi\s+atras|correr\s+atras)\b`,
		`\b(tem\s+sentimentos?|tinha\s+sentimentos?|ter\s+sentimentos?)\b`,
		`\b(esta\s+apaixonad[oa]|ficou\s+apaixonad[oa]|apaixonou)\b`,
		`\b(tem\s+interesse|demonstrou?\s+interesse|esta\s+interessad[oa])\b`,
		`\b(curte|curtiu|se\s+apaixonou|quer\s+fic[ao]r?|quer\s+namorar)\b`,
		`\b(esta\s+(gostando|querendo)|sempre\s+gostou)\b`,
	}
	for _, p := range patterns {
		if regexp.MustCompile(p).MatchString(t) {
			return true
		}
	}
	return false
}

func getCategoryLabel(category string) string {
	if label, ok := CategoryLabels[category]; ok {
		return label
	}
	return CategoryLabels["OUTRO"]
}

// --- Utilities ---

func buildRecentChatBlock(buf []monitor.ChatMessage) string {
	if len(buf) == 0 {
		return "(nenhuma mensagem anterior no buffer)"
	}
	end := len(buf)
	start := end - recentChatLimit
	if start < 0 {
		start = 0
	}
	var lines []string
	for _, m := range buf[start:end] {
		name := coalesceStr(m.Nickname, m.UniqueID, "?")
		lines = append(lines, fmt.Sprintf("%s: %s", name, m.Comment))
	}
	return strings.Join(lines, "\n")
}

func jsonString(s string) string {
	// Simple JSON quoting.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `"` + s + `"`
}

func coalesceStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func coalesceEnv(key, fallback string) string {
	if v := strings.TrimSpace(key); v != "" {
		return v
	}
	return fallback
}

func isTruthy(s string) bool {
	return s == "1" || s == "true" || s == "yes" || s == "y"
}

func logAudit(event, liveName, uniqueID, nickname, raw, originalCategory, finalCategory, message string) {
	if !auditEnabled {
		return
	}
	payload := map[string]interface{}{
		"liveName":         liveName,
		"uniqueId":         uniqueID,
		"nickname":         nickname,
		"rawModelOutput":   raw,
		"originalCategory": originalCategory,
		"finalCategory":    finalCategory,
		"message":          message,
	}
	log.Printf("[MOD-AUDIT] %s: %v", event, payload)
}
