package execution

import "testing"

func TestManagedEnvironment(t *testing.T) {
	for _, env := range []Environment{EnvironmentSandbox, EnvironmentLive} {
		if !env.Valid() {
			t.Fatalf("valid environment rejected: %q", env)
		}
	}
	for _, env := range []Environment{"", "production", "TESTNET", "demo"} {
		if env.Valid() {
			t.Fatalf("invalid environment accepted: %q", env)
		}
	}
	if ErrUnsupported == nil {
		t.Fatal("unsupported capability must be reportable")
	}
	var _ = Capabilities{SafeStopAmend: false, PrivateEvents: false}
}
