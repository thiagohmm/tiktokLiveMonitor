package model

import "testing"

func TestGiftValueKnown(t *testing.T) {
	cases := map[string]int{
		"Rose":                     1,
		"rose":                     1,
		"Rosa":                     1,
		"Heart":                    1,
		"Coração":                  1,
		"Finger Heart":             5,
		"Coração de Dedo":          5,
		"Hand Hearts":              100,
		"Hand Heart":               100,
		"Coração de Mão":           100,
		"Confetti":                 100,
		"Confete":                  100,
		"Sunglasses":               199,
		"Óculos de Sol":            199,
		"Galaxy":                   1000,
		"Galáxia":                  1000,
		"Fireworks":                1088,
		"Fogos de Artifício":       1088,
		"Rocket":                   500,
		"Foguete":                  500,
		"Money Gun":                500,
		"Metralhadora de Dinheiro": 500,
		"Drama Queen":              5000,
		"Rainha do Drama":          5000,
		"Phoenix":                  25999,
		"Fênix":                    25999,
		"Lion":                     29999,
		"Leão":                     29999,
		"Castle":                   20000,
		"Castelo":                  20000,
		"TikTok Universe":          44999,
	}
	for name, want := range cases {
		if got := GiftValue(name); got != want {
			t.Errorf("GiftValue(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestGiftValueUnknownFallsBackToOne(t *testing.T) {
	for _, name := range []string{"", "  ", "Unknown Gift", "presente misterioso", "Presente 12345"} {
		if got := GiftValue(name); got != defaultGiftValue {
			t.Errorf("GiftValue(%q) = %d, want fallback %d", name, got, defaultGiftValue)
		}
	}
}
