package monitor

import (
	"testing"
)

func TestNormalizeID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  User1  ", "user1"},
		{"UPPER", "upper"},
		{"MiXeD", "mixed"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeID(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeID(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFoldText(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  Hello World  ", "hello world"},
		{"Voce gosta?", "voce gosta?"},
		{"COMO ASSIM", "como assim"},
		{"", ""},
	}
	for _, tt := range tests {
		got := foldText(tt.input)
		if got != tt.expected {
			t.Errorf("foldText(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestLooksLikeQuestion(t *testing.T) {
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
		"¿Que hora es?",
	}
	for _, q := range questions {
		if !looksLikeQuestion(q) {
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
		if looksLikeQuestion(q) {
			t.Errorf("expected %q NOT to be a question", q)
		}
	}
}

func TestDetectKeyword(t *testing.T) {
	m := &Monitor{
		settings: Settings{
			TargetGifts: []string{"dino", "perfume"},
		},
	}
	tests := []struct {
		input    string
		expected string
	}{
		{"O dino apareceu", "dino"},
		{"Envia perfume", "perfume"},
		{"DINO gigante", "dino"},
		{"Boa live", ""},
		{"Oi pessoal", ""},
	}
	for _, tt := range tests {
		got := m.detectKeyword(tt.input)
		if got != tt.expected {
			t.Errorf("detectKeyword(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsTargetGift(t *testing.T) {
	m := &Monitor{
		settings: Settings{
			TargetGifts: []string{"perfume", "coração", "dino"},
		},
	}
	targets := []string{
		"Perfume",
		"Dino",
		"coração",
		"tiny dino",
	}
	for _, g := range targets {
		if !m.isTargetGift(g) {
			t.Errorf("expected %q to be a target gift", g)
		}
	}

	notTargets := []string{
		"Rose",
		"Tiger",
		"Coffee",
		"Galaxy",
	}
	for _, g := range notTargets {
		if m.isTargetGift(g) {
			t.Errorf("expected %q NOT to be a target gift", g)
		}
	}
}

func TestCoalesce(t *testing.T) {
	tests := []struct {
		input    []string
		expected string
	}{
		{[]string{"", "b", "c"}, "b"},
		{[]string{"", "", ""}, ""},
		{[]string{"first"}, "first"},
		{[]string{}, ""},
	}
	for _, tt := range tests {
		got := coalesce(tt.input...)
		if got != tt.expected {
			t.Errorf("coalesce(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestCoalesceStr(t *testing.T) {
	tests := []struct {
		val      string
		fallback string
		expected string
	}{
		{"hello", "world", "hello"},
		{"", "world", "world"},
		{"", "", ""},
	}
	for _, tt := range tests {
		got := coalesceStr(tt.val, tt.fallback)
		if got != tt.expected {
			t.Errorf("coalesceStr(%q, %q) = %q, want %q", tt.val, tt.fallback, got, tt.expected)
		}
	}
}
