package moderation

import (
	"testing"
)

func TestFoldText(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  Hello World  ", "hello world"},
		{"Voce gosta?", "voce gosta?"},
		{"COMO ASSIM", "como assim"},
		{"cafe com acar", "cafe com acar"},
		{"", ""},
	}
	for _, tt := range tests {
		got := foldText(tt.input)
		if got != tt.expected {
			t.Errorf("foldText(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestTruncate(t *testing.T) {
	s := "hello world"
	if truncate(s, 100) != s {
		t.Error("truncate should not modify short string")
	}
	got := truncate(s, 5)
	if got != "hello" {
		t.Errorf("truncate(%q, 5) = %q, want %q", s, got, "hello")
	}
}

func TestLooksQuestion(t *testing.T) {
	questions := []string{
		"Qual sua religiao?",
		"Como vai?",
		"Pq isso?",
		"PK isso?",
		"Por que voce fez isso",
		"Tem como me ajudar?",
		"Da pra explicar?",
		"Alguem sabe a resposta",
		"Me tira uma duvida",
		"Qual o preco?",
		"Qual a hora?",
	}
	for _, q := range questions {
		if !looksQuestion(q) {
			t.Errorf("expected %q to be a question", q)
		}
	}

	notQuestions := []string{
		"Boa noite",
		"Oi pessoal",
		"Show a live",
		"Valeu streamer",
		"",
	}
	for _, q := range notQuestions {
		if looksQuestion(q) {
			t.Errorf("expected %q NOT to be a question", q)
		}
	}
}

func TestPassesChristianProselytizingAiGate(t *testing.T) {
	positive := []string{
		"Jesus salva",
		"Aceite Jesus e tenha salvacao",
		"Igreja e a resposta",
		"O pastor disse",
	}
	for _, c := range positive {
		if !passesChristianProselytizingAiGate(c) {
			t.Errorf("expected %q to pass the gate", c)
		}
	}

	negative := []string{
		"Boa noite pessoal",
		"Show a live",
		"Valeu streamer",
	}
	for _, c := range negative {
		if passesChristianProselytizingAiGate(c) {
			t.Errorf("expected %q NOT to pass the gate", c)
		}
	}
}

func TestHasExternalShortlinkOrMessenger(t *testing.T) {
	positive := []string{
		"bit.ly/123",
		"tinyurl.com/abc",
		"wa.me/551199999",
		"t.me/channel",
		"telegram.me/bot",
	}
	for _, c := range positive {
		if !hasExternalShortlinkOrMessenger(c) {
			t.Errorf("expected %q to have shortlink", c)
		}
	}

	negative := []string{
		"tiktok.com/@user",
		"vm.tiktok.com/abc",
		"Boa noite",
	}
	for _, c := range negative {
		if hasExternalShortlinkOrMessenger(c) {
			t.Errorf("expected %q NOT to have shortlink", c)
		}
	}
}

func TestHasNonTiktokHttpLink(t *testing.T) {
	positive := []string{
		"Check https://google.com",
		"Visit www.example.com now",
	}
	for _, c := range positive {
		if !hasNonTiktokHttpLink(c) {
			t.Errorf("expected %q to have non-tiktok link", c)
		}
	}

	negative := []string{
		"tiktok.com/@user",
		"vm.tiktok.com/abc",
		"Boa noite",
	}
	for _, c := range negative {
		if hasNonTiktokHttpLink(c) {
			t.Errorf("expected %q NOT to have non-tiktok link", c)
		}
	}
}

func TestPassesSpamScamAiGate(t *testing.T) {
	positive := []string{
		"pix qrcode aqui",
		"clica no link da bio",
		"ganhe dinheiro facil",
		"bit.ly/123",
		"https://google.com curso gratis",
	}
	for _, c := range positive {
		folded := foldText(c)
		if !passesSpamScamAiGate(c, folded) {
			t.Errorf("expected %q to pass spam gate", c)
		}
	}

	negative := []string{
		"Boa noite",
		"Show a live",
		"Valeu streamer",
	}
	for _, c := range negative {
		folded := foldText(c)
		if passesSpamScamAiGate(c, folded) {
			t.Errorf("expected %q NOT to pass spam gate", c)
		}
	}
}

func TestPassesPersonalAttackAiGate(t *testing.T) {
	positive := []string{
		"voce e um idiota",
		"filho da puta",
		"vai tomar no cu",
		"morre agora",
		"cala a boca",
		"testuda",
		"sua mae e puta",
	}
	for _, c := range positive {
		folded := foldText(c)
		if !passesPersonalAttackAiGate(folded) {
			t.Errorf("expected %q to pass personal attack gate", c)
		}
	}

	negative := []string{
		"Boa noite pessoal",
		"Show a live",
		"Valeu streamer",
	}
	for _, c := range negative {
		folded := foldText(c)
		if passesPersonalAttackAiGate(folded) {
			t.Errorf("expected %q NOT to pass personal attack gate", c)
		}
	}
}

func TestGetCategoryLabel(t *testing.T) {
	tests := []struct {
		cat  string
		want string
	}{
		{"PROSELITISMO", "Proselitismo Cristao"},
		{"SPAM", "Spam / propaganda (IA)"},
		{"GOLPE", "Possivel golpe ou fraude (IA)"},
		{"ODIO", "Odio / insulto grave (IA)"},
		{"PERGUNTA", "Pergunta / Duvida (IA)"},
		{"UNKNOWN", "Conteudo improprio (IA)"},
	}
	for _, tt := range tests {
		got := getCategoryLabel(tt.cat)
		if got == "" {
			t.Errorf("getCategoryLabel(%q) returned empty", tt.cat)
		}
	}
}

func TestCategoryLabels(t *testing.T) {
	expected := []string{"PROSELITISMO", "SPAM", "GOLPE", "ODIO", "PERGUNTA", "OUTRO"}
	for _, cat := range expected {
		if _, ok := CategoryLabels[cat]; !ok {
			t.Errorf("missing CategoryLabels entry for %q", cat)
		}
	}
}

func TestCoalesceStr(t *testing.T) {
	tests := []struct {
		input    []string
		expected string
	}{
		{[]string{"a", "b"}, "a"},
		{[]string{"", "b"}, "b"},
		{[]string{"", ""}, ""},
	}
	for _, tt := range tests {
		got := coalesceStr(tt.input...)
		if got != tt.expected {
			t.Errorf("coalesceStr(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
func TestClassifyByRules(t *testing.T) {
	tests := []struct {
		name     string
		comment  string
		wantFlag bool
		wantCat  string
	}{
		{name: "normal", comment: "boa noite pessoal", wantFlag: false, wantCat: "OK"},
		{name: "question", comment: "qual a sua musica favorita?", wantFlag: false, wantCat: "PERGUNTA"},
		{name: "proselytizing", comment: "jesus salva, aceita a cristo", wantFlag: true, wantCat: "PROSELITISMO"},
		{name: "spam link", comment: "clica no link da bio https://bit.ly/abc", wantFlag: true, wantCat: "SPAM"},
		{name: "insult", comment: "voce e um idiota", wantFlag: true, wantCat: "ODIO"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := classifyByRules(tt.comment, foldText(tt.comment))
			if res.Flagged != tt.wantFlag {
				t.Errorf("Flagged = %v, want %v", res.Flagged, tt.wantFlag)
			}
			if res.Category != tt.wantCat {
				t.Errorf("Category = %q, want %q", res.Category, tt.wantCat)
			}
		})
	}
}
