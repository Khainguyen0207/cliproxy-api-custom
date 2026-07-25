package util

import "testing"

func TestMaskSensitiveHeaderValueMasksCookies(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{
			name:  "cookie",
			key:   "Cookie",
			value: "session=very-secret-cookie-value",
		},
		{
			name:  "set cookie",
			key:   "Set-Cookie",
			value: "__cf_bm=very-secret-cookie-value; HttpOnly; Secure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaskSensitiveHeaderValue(tt.key, tt.value)
			if got == tt.value {
				t.Fatalf("expected %s to be masked", tt.key)
			}
			if got == "" {
				t.Fatalf("expected non-empty masked value")
			}
		})
	}
}
