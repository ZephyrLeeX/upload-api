package auth

import "testing"

func TestValidateBearer(t *testing.T) {
	expected := "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name, header string
		want         bool
	}{
		{"correct", "Bearer " + expected, true},
		{"wrong", "Bearer 0123456789abcdef0123456789abcdee", false},
		{"missing", "", false},
		{"basic", "Basic " + expected, false},
		{"empty bearer", "Bearer ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateBearer(tt.header, expected); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
