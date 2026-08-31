package model

import "strings"

// defaultGiftValue is applied to gifts that are not in the researched price
// table: 1 coin (the value of a Rose, the cheapest TikTok live gift).
const defaultGiftValue = 1

// Gift coin values (diamonds), researched online (Aug/2026) from public
// TikTok live gift charts (streamwrapped.com/tiktok-gifts,
// ttcalculator.net/learn/live-gifts-value-chart and bettertok.app). The value
// is the number of coins a viewer spends per unit of the gift — the same
// metric behind TikTok's in-room live ranking.
var giftValuesEnglish = map[string]int{
	// Cheap
	"rose": 1, "tiktok": 1, "heart": 1, "gg": 1, "thumbs up": 1,
	"ice cream cone": 1, "glow stick": 1, "coffee": 1,
	"team bracelet": 2,
	"hidden egg":    3,
	"finger heart":  5, "panda": 5, "mic": 5, "microphone": 5,
	"duckling": 5, "peach": 5,
	"carnation": 7, "waving hand": 7,
	"cheer you up": 9, "applause": 9,
	"lollipop": 10, "dog bone": 10, "gamepad": 10, "game controller": 10,
	"raccoon":  15,
	"baby fox": 20, "perfume": 20,
	"capybara": 30,
	"donut":    30, "doughnut": 30, "donuts": 30, "doughnuts": 30,
	"i love you":  49,
	"tea":         50,
	"butterfly":   88,
	"paper crane": 99, "little crown": 99, "cap": 99, "hat": 99, "guitar": 99,
	"confetti": 100, "hand hearts": 100, "hand heart": 100, "bouquet": 100,
	"umbrella": 150, "kiss": 150,
	"musical notes": 169,
	"crown":         199, "hearts": 199, "sunglasses": 199, "pug": 199,
	"birthday cake": 300, "cake": 300,
	"astronaut": 299, "boxing gloves": 299, "corgi": 299,
	"beating heart": 449,
	"coral":         499, "magic potion": 499,
	"rocket": 500, "make it rain": 500, "money rain": 500,
	"money gun": 500, "money": 500, "trophy": 500,
	"record player": 600,
	"swan":          699, "love balloon": 699, "dance together": 699,
	"pearl": 800, "handbag": 800,
	"train":  899,
	"galaxy": 1000, "disco ball": 1000, "disco": 1000, "disc": 1000,
	"dragon": 1000, "magic lamp": 1000, "gold mine": 1000, "dinosaur": 1000,
	"fireworks":    1088,
	"diamond":      1099,
	"gaming chair": 1200,
	"diamond ring": 1500, "champion": 1500,
	"tree house": 1799,
	"airship":    1999, "rabbit": 1999, "bunny": 1999,
	"carousel":     2020,
	"whale diving": 2150,
	"jet ski":      2199,
	"music box":    2399,
	"concert":      2888,
	"mermaid":      2988, "motorcycle": 2988,
	"superstars":    2999,
	"meteor shower": 3000, "ferris wheel": 3000, "dancing bears": 3000,
	"sakura train":   3999,
	"tiktok volcano": 4000,
	"knight":         4088,
	"tractor":        4099,
	"pirate ship":    4888, "private jet": 4888, "leon the kitten": 4888,
	"pool party":      4999,
	"unicorn fantasy": 5000, "unicorn": 5000, "wolf": 5000,
	"dancing adam": 5000, "drama queen": 5000,
	"submarine": 5199,
	"airplane":  6000, "double decker": 6000, "starfish bay": 6000,
	"sports car":    7000,
	"yacht":         7499,
	"monster truck": 7999,
	"leon and lili": 9699,
	"aquarius":      9999,
	"interstellar":  10000, "octopus": 10000,
	"falcon":          10999,
	"convertible car": 12000, "frog prince": 12000, "mountains": 12000,
	"spaceship":      13999,
	"castle skyline": 15000, "planet": 15000, "peacock": 15000,
	"leopard": 15000, "pyramids": 15000,
	"diamond flight": 18000,
	"party boat":     19999,
	"castle fantasy": 20000, "castle": 20000, "tiktok shuttle": 20000,
	"adam's dream": 25999, "phoenix": 25999, "griffin": 25999,
	"dragon flame": 26999,
	"lion":         29999,
	"gorilla":      30000, "sam the whale": 30000, "whale": 30000,
	"zeus":           34000,
	"thunder falcon": 39999, "tiktok stars": 39999,
	"legend marcellus":    42999,
	"julius the champion": 43999,
	"tiktok universe":     44999, "universe": 44999,
}

// giftValuesPortuguese mirrors giftValuesEnglish for the names stored in the
// database, which are the translated (Brazilian Portuguese) variants used by
// the gift name translation table (monitor/gifts.js + controller/gifts.go).
// Keys are accent-insensitive (see normalizeGiftValueKey).
var giftValuesPortuguese = map[string]int{
	"rosa":                     1,     // Rose
	"coracao":                  1,     // Heart
	"coracao de dedo":          5,     // Finger Heart
	"coracao de mao":           100,   // Hand Heart
	"coracao de amor":          1,     // Love Heart
	"coracao batendo":          449,   // Beating Heart
	"batimento":                449,   // Beating Heart
	"oculos de sol":            199,   // Sunglasses
	"espelho":                  100,   // Mirror
	"borboleta":                88,    // Butterfly
	"bone":                     99,    // Cap
	"chapeu":                   99,    // Hat
	"metralhadora de dinheiro": 500,   // Money Gun
	"galaxia":                  1000,  // Galaxy
	"leao":                     29999, // Lion
	"helicoptero":              800,   // Helicopter
	"carro de corrida":         1000,  // Race Car
	"carro esportivo":          7000,  // Sports Car
	"foguete":                  500,   // Rocket
	"castelo":                  20000, // Castle
	"coroa":                    199,   // Crown
	"baleia":                   30000, // Whale
	"golfinho":                 10,    // Dolphin
	"rainha do drama":          5000,  // Drama Queen
	"unicornio":                5000,  // Unicorn
	"dragao":                   1000,  // Dragon
	"fenix":                    25999, // Phoenix
	"eu te amo":                49,    // I Love You
	"palmas":                   9,     // Applause
	"confete":                  100,   // Confetti
	"fogos de artificio":       1088,  // Fireworks
	"beijo":                    150,   // Kiss
	"disco":                    1000,  // Disc
	"discoteca":                1000,  // Disco
	"presente":                 1,     // Gift
	"moeda":                    10,    // Coin
	"dinheiro":                 500,   // Money
	"caixa de presente":        200,   // Gift Box
	"bolsa":                    800,   // Handbag
	"perfume":                  20,    // Perfume
	"buque":                    100,   // Bouquet
	"microfone":                5,     // Microphone
	"trofeu":                   500,   // Trophy
	"diamante":                 1099,  // Diamond
}

// GiftValue returns the coin (diamond) value of one unit of the given gift.
// It accepts both the English name and the translated Portuguese name;
// unknown gifts fall back to defaultGiftValue (1 coin, the Rose baseline).
func GiftValue(name string) int {
	key := normalizeGiftValueKey(name)
	if key == "" {
		return defaultGiftValue
	}
	if v, ok := giftValuesEnglish[key]; ok {
		return v
	}
	if v, ok := giftValuesPortuguese[key]; ok {
		return v
	}
	return defaultGiftValue
}

// normalizeGiftValueKey lowercases, trims and removes accents so accented
// Portuguese names ("Galáxia", "Fênix", "Óculos de Sol") match table keys.
func normalizeGiftValueKey(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}
	var out []rune
	for _, r := range key {
		switch r {
		case 'á', 'à', 'â':
			r = 'a'
		case 'ã':
			r = 'a'
		case 'é', 'ê':
			r = 'e'
		case 'í', 'ì', 'î':
			r = 'i'
		case 'ó', 'ò', 'ô':
			r = 'o'
		case 'õ':
			r = 'o'
		case 'ú', 'ù', 'ü':
			r = 'u'
		case 'ç':
			r = 'c'
		case 'ñ':
			r = 'n'
		case 0x0300, 0x0301, 0x0302, 0x0303, 0x0304, 0x0305, 0x0306, 0x0307, 0x0308, 0x0309, 0x0323, 0x0327:
			continue // combining accents/tildes
		}
		out = append(out, r)
	}
	return string(out)
}
