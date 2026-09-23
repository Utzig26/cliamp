package config

import "testing"

func TestNormalizeCoverArtSize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"small", CoverArtSmall},
		{"SMALL", CoverArtSmall},
		{"  large  ", CoverArtLarge},
		{"medium", CoverArtMedium},
		{"", CoverArtMedium},
		{"enormous", CoverArtMedium},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NormalizeCoverArtSize(tt.in); got != tt.want {
				t.Errorf("NormalizeCoverArtSize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
