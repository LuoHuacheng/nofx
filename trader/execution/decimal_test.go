package execution

import "testing"

func TestBalanceDecimalFloorsWithoutChangingOrderPrecision(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"796.2634999999999999", "796.263499999999"},
		{"213.66", "213.66"},
		{"0.0000000000009", "0"},
		{"-0.0000000000009", "-0.000000000001"},
		{"0", "0"},
	} {
		got, err := BalanceDecimal(tc.input)
		if err != nil || string(got) != tc.want {
			t.Fatalf("%s: got %s, %v; want %s", tc.input, got, err, tc.want)
		}
	}
	for _, invalid := range []string{"", "NaN", "1e3", "1/2", "1.2.3"} {
		if _, err := BalanceDecimal(invalid); err == nil {
			t.Fatalf("accepted invalid balance %q", invalid)
		}
	}
	r, _ := Number("796.2634999999999999")
	if _, err := ExactDecimal(r); err == nil {
		t.Fatal("execution prices and quantities must still reject rounding")
	}
}

func TestMarketCloseReferenceAllowsWeightedAverageOnly(t *testing.T) {
	o := Order{ClientID: "00112233445566778899aabbccddeeff", Role: "entry", Type: "market", ReduceOnly: true, Side: "long", Quantity: "12257", Price: "0.1287878419678551"}
	if err := ValidateIntent(o); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"limit", "sl", "entry"} {
		invalid := o
		switch kind {
		case "limit":
			invalid.Type = "limit"
		case "sl":
			invalid.Role = "sl"
		case "entry":
			invalid.ReduceOnly = false
		}
		if ValidOrderPrice(invalid) {
			t.Fatal("relaxed actual order-price validation", kind)
		}
	}
	for _, price := range []Decimal{"NaN", "Infinity", "-1", "0", "1/2", "1e-5", "", "0.1.2"} {
		o.Price = price
		if ValidOrderPrice(o) {
			t.Fatal("invalid reference accepted", price)
		}
	}
}
