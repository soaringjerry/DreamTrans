package handlers

import (
	"strings"
	"testing"
)

func TestParseSessionCostIDs(t *testing.T) {
	first := "11111111-1111-4111-8111-111111111111"
	second := "22222222-2222-4222-8222-222222222222"

	ids, err := parseSessionCostIDs(first + ", " + second + ",," + first)
	if err != nil {
		t.Fatalf("valid list rejected: %v", err)
	}
	if len(ids) != 2 || ids[0] != first || ids[1] != second {
		t.Fatalf("got %v, want deduplicated [%s %s]", ids, first, second)
	}

	if _, err := parseSessionCostIDs(""); err == nil {
		t.Fatal("empty parameter was accepted")
	}
	if _, err := parseSessionCostIDs(first + ",not-a-uuid"); err == nil {
		t.Fatal("non-UUID id was accepted")
	}
	if _, err := parseSessionCostIDs(first + ",'; DROP TABLE usage_logs;--"); err == nil {
		t.Fatal("injection-shaped id was accepted")
	}

	many := make([]string, 0, maxSessionCostIDs+1)
	for i := 0; i <= maxSessionCostIDs; i++ {
		many = append(many, uuidWithSuffix(i))
	}
	if _, err := parseSessionCostIDs(strings.Join(many, ",")); err == nil {
		t.Fatalf("more than %d ids were accepted", maxSessionCostIDs)
	}
}

// uuidWithSuffix builds distinct, valid UUID strings for bound tests.
func uuidWithSuffix(n int) string {
	suffix := []byte("000000000000")
	digits := []byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9'}
	for i := len(suffix) - 1; i >= 0 && n > 0; i-- {
		suffix[i] = digits[n%10]
		n /= 10
	}
	return "33333333-3333-4333-8333-" + string(suffix)
}
