package wallet

import "testing"

func TestDecodeUSDCBalance(t *testing.T) {
	for _, tc := range []struct {
		body    string
		want    float64
		invalid bool
	}{
		{`{"result":"0x0"}`, 0, false},
		{`{"result":"0xf4240"}`, 1, false},
		{`{"result":"0x1"}`, 0.000001, false},
		{`{}`, 0, true},
		{`{"result":"0x"}`, 0, true},
		{`{"result":"garbage"}`, 0, true},
		{`{"result":"0xzz"}`, 0, true},
		{`{"error":{"code":-32000}}`, 0, true},
	} {
		got, err := decodeUSDCBalance([]byte(tc.body))
		if (err != nil) != tc.invalid || got != tc.want {
			t.Errorf("%s: got %v, error %v", tc.body, got, err)
		}
	}
}
