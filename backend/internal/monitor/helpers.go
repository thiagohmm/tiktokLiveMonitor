package monitor

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}

func extractFromData(data EventData) UserInfo {
	info := UserInfo{
		UniqueID: asString(data["uniqueId"]),
		Nickname: asString(data["nickname"]),
	}
	if info.Nickname == "" {
		info.Nickname = info.UniqueID
	}
	if f, ok := parseFollowerFlag(data["isFollower"]); ok {
		info.IsFollower = f
	}
	return info
}

func asString(v interface{}) string {
	switch n := v.(type) {
	case string:
		return strings.TrimSpace(n)
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case json.Number:
		return strings.TrimSpace(n.String())
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	default:
		return ""
	}
}

func parseFollowerFlag(v interface{}) (*bool, bool) {
	switch n := v.(type) {
	case bool:
		b := n
		return &b, true
	case float64:
		if n == 1 || n == 2 {
			b := true
			return &b, true
		}
		if n == 0 {
			b := false
			return &b, true
		}
	case string:
		switch strings.TrimSpace(strings.ToLower(n)) {
		case "true", "1", "2":
			b := true
			return &b, true
		case "false", "0":
			b := false
			return &b, true
		}
	}
	return nil, false
}

func normalizeID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func foldText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if r == 'ç' {
			return 'c'
		}
		if r >= 0x0300 && r <= 0x036F {
			return -1
		}
		return r
	}, s)
	return s
}

func looksLikeQuestion(comment string) bool {
	raw := strings.TrimSpace(comment)
	if raw == "" {
		return false
	}
	if strings.ContainsAny(raw, "?¿") {
		return true
	}
	t := foldText(raw)
	questionStarts := regexp.MustCompile(
		`^(pq|pk|por\s+que|porque|como|quando|onde|aonde|quem|qual|quais|sera\s+que|duvida\b|duvida[:\\-])`,
	)
	if questionStarts.MatchString(t) {
		return true
	}
	questionCues := regexp.MustCompile(
		`\b(tem\s+como|da\s+pra|d[aá]\s+pra|alguem\s+sabe|algm\s+sabe|me\s+tira\s+uma\s+duvida|qual\s+o|qual\s+a)\b`,
	)
	return questionCues.MatchString(t)
}

func (m *Monitor) detectKeyword(comment string) string {
	lower := strings.ToLower(comment)
	for _, target := range m.settings.TargetGifts {
		tLower := strings.ToLower(target)
		if strings.Contains(lower, tLower) {
			return tLower
		}
	}
	return ""
}

func (m *Monitor) isTargetGift(name string) bool {
	lower := strings.ToLower(name)
	compact := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, lower)

	for _, target := range m.settings.TargetGifts {
		tLower := strings.ToLower(target)
		tCompact := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, tLower)

		if strings.Contains(lower, tLower) || strings.Contains(compact, tCompact) {
			return true
		}
	}
	return false
}

func truthy(v interface{}) bool {
	switch n := v.(type) {
	case bool:
		return n
	case float64:
		return n != 0
	case int:
		return n != 0
	case int64:
		return n != 0
	case string:
		s := strings.TrimSpace(strings.ToLower(n))
		return s != "" && s != "false" && s != "0"
	default:
		return false
	}
}

// parseStoredTimestampMillis converts a DB timestamp string to Unix milliseconds.
// Falls back to fallback when parsing fails.
func parseStoredTimestampMillis(raw string, fallback int64) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UnixMilli()
		}
	}
	return fallback
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func coalesceStr(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
