package database

import "testing"

func TestRebindQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "SELECT * FROM t WHERE a = ?", "SELECT * FROM t WHERE a = $1"},
		{"two params", "SELECT * FROM t WHERE a = ? AND b = ?", "SELECT * FROM t WHERE a = $1 AND b = $2"},
		{"question in literal", "SELECT * FROM t WHERE instr(a, '?') > 0 AND b = ?", "SELECT * FROM t WHERE instr(a, '?') > 0 AND b = $1"},
		{"empty literal", "SELECT * FROM t WHERE a = '' AND b = ?", "SELECT * FROM t WHERE a = '' AND b = $1"},
		{"escaped quote", "SELECT * FROM t WHERE a = 'it''s ?' AND b = ?", "SELECT * FROM t WHERE a = 'it''s ?' AND b = $1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rebindQuery(tc.in); got != tc.want {
				t.Fatalf("rebindQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
