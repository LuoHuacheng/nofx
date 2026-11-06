package execution

import (
	"strings"
	"testing"
)

func TestCanonicalClientIDsPreserveDurableIDsAndLegacyLabels(t *testing.T) {
	raw := strings.Repeat("ab", 16)
	a, err := CanonicalClientID(raw)
	if err != nil || a != raw {
		t.Fatal(a, err)
	}
	a, err = CanonicalClientID("grid-1-123")
	if err != nil || len(a) != 32 {
		t.Fatal(a, err)
	}
	b, _ := CanonicalClientID("grid-1-123")
	c, _ := CanonicalClientID("grid-2-123")
	if a != b || a == c {
		t.Fatal("legacy IDs are not stable or distinct")
	}
	a, err = CanonicalClientID("")
	if err != nil {
		t.Fatal(err)
	}
	b, err = CanonicalClientID("")
	if err != nil || len(a) != 32 || a == b {
		t.Fatal("missing IDs must be unique", err)
	}
}
func TestExactOrderNumbersRejectInvalidWireValues(t *testing.T) {
	for _, s := range []Decimal{"NaN", "Inf", "1/2", "1e3", "-1", "0", "0.0000000000001"} {
		if Positive(s) {
			t.Errorf("accepted %q", s)
		}
	}
	for _, s := range []Decimal{"0.000000000001", "123456.1200"} {
		if !Positive(s) {
			t.Errorf("rejected %q", s)
		}
	}
}
