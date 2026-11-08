package config

import "testing"

func TestFastPollingAllowed_DefaultsOn(t *testing.T) {
	if !(WritesConfig{}).FastPollingAllowed() {
		t.Error("allow_fast_polling omitted should default to true")
	}
	if (WritesConfig{AllowFastPolling: BoolPtr(false)}).FastPollingAllowed() {
		t.Error("allow_fast_polling=false should disable Service Mode writes")
	}
}
