// Package moderation provides the message analysis pipeline using rule-based classifiers.
package moderation

import (
	"context"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

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
	cacheMax = 150
)

// Engine handles message moderation.
type Engine struct {
	mu           sync.Mutex
	repo         model.Repository
	cache        map[string]AnalysisResult
	warmupStatus StartupStatus
	warmupFlight *sync.Mutex
	allowlist    map[string]struct{}
}

// NewEngine creates a moderation engine.
func NewEngine(repo model.Repository) *Engine {
	return &Engine{
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

// ClearCache resets the moderation cache.
func (e *Engine) ClearCache() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache = make(map[string]AnalysisResult)
}

// --- Text Processing Helpers ---

// WarmupLearning prepares the moderation pipeline (allowlist refresh). The LLM
// warmup moved to the Python agent (docs/plano-unificacao-ia.md, fase 2).
func (e *Engine) WarmupLearning(ctx context.Context, force bool) (StartupStatus, error) {
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

	e.refreshAllowlist()

	e.mu.Lock()
	e.warmupStatus = StartupStatus{
		Ready:    true,
		WarmedAt: time.Now().UTC().Format(time.RFC3339),
	}
	e.mu.Unlock()

	return e.warmupStatus, nil
}

// AnalyzeMessage classifies a chat message using deterministic rules. The LLM
// classification moved to the Python agent (docs/plano-unificacao-ia.md, fase 2).
func (e *Engine) AnalyzeMessage(ctx context.Context, comment, uniqueID, nickname string, chatBuf []monitor.ChatMessage, liveName string) (AnalysisResult, error) {
	commentLower := strings.TrimSpace(strings.ToLower(comment))
	folded := foldText(commentLower)

	e.mu.Lock()
	if _, ok := e.allowlist[folded]; ok {
		e.mu.Unlock()
		log.Printf("[Moderation] ✅ liberado por allowlist: %q", truncate(comment, 50))
		return AnalysisResult{Flagged: false, Category: "OK"}, nil
	}

	// Cache check.
	cacheKey := truncate(folded, 500)
	if cached, ok := e.cache[cacheKey]; ok {
		e.mu.Unlock()
		return cached, nil
	}
	e.mu.Unlock()

	result := classifyByRules(comment, folded)

	// Store in cache.
	e.mu.Lock()
	e.cache[cacheKey] = result
	if len(e.cache) > cacheMax {
		for k := range e.cache {
			delete(e.cache, k)
			break
		}
	}
	e.mu.Unlock()

	if result.Flagged {
		log.Printf("[Moderation] ⚠️ CONTEÚDO FLAGADO: [%s] - %q", result.Category, comment)
	} else {
		preview := truncate(comment, 50)
		log.Printf("[Moderation] ✅ Conteúdo liberado: %q", preview)
	}

	// Log to database asynchronously.
	go func() {
		if err := e.repo.LogAnomaly(liveName, comment, result.Flagged, result.Category, uniqueID); err != nil {
			log.Printf("[Database] Erro ao logar anomalia: %v", err)
		}
	}()

	return result, nil
}

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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// --- Rule-based Classifiers ---

// classifyByRules classifies a comment with deterministic rules. Priority
// follows the old LLM prompt: personal attacks win over questions.
func classifyByRules(comment, folded string) AnalysisResult {
	if passesPersonalAttackAiGate(folded) {
		return AnalysisResult{Flagged: true, Category: "ODIO", Reason: getCategoryLabel("ODIO")}
	}
	if passesChristianProselytizingAiGate(comment) {
		return AnalysisResult{Flagged: true, Category: "PROSELITISMO", Reason: getCategoryLabel("PROSELITISMO")}
	}
	if passesSpamScamAiGate(comment, folded) {
		return AnalysisResult{Flagged: true, Category: "SPAM", Reason: getCategoryLabel("SPAM")}
	}
	if looksQuestion(comment) {
		return AnalysisResult{Flagged: false, Category: "PERGUNTA"}
	}
	return AnalysisResult{Flagged: false, Category: "OK"}
}

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

func getCategoryLabel(category string) string {
	if label, ok := CategoryLabels[category]; ok {
		return label
	}
	return CategoryLabels["OUTRO"]
}

// --- Utilities ---

func coalesceStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
