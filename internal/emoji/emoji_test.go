package emoji

import "testing"

func TestAssetIDRecognizesStandardClusters(t *testing.T) {
	tests := []struct {
		name    string
		cluster string
		wantID  string
		wantOK  bool
	}{
		{"plain ASCII", "a", "", false},
		{"bare keycap digit", "1", "", false},
		{"single emoji", "👍", "1f44d", true},
		{"emoji with variation selector", "✈️", "2708", true},
		{"skin tone modifier", "👍🏻", "1f44d-1f3fb", true},
		{"zwj sequence", "👨‍👩‍👧‍👦", "1f468-200d-1f469-200d-1f467-200d-1f466", true},
		{"flag pair", "🇫🇮", "1f1eb-1f1ee", true},
		{"keycap with variation selector", "1️⃣", "31-20e3", true},
		{"keycap without variation selector", "1⃣", "31-20e3", true},
		{"consecutive modifiers", "👍🏻🏻", "", false},
		{"modifier without base", "🏻", "", false},
		{"trailing zwj", "👍‍", "", false},
		{"leading zwj", "‍👍", "", false},
		{"two bases without zwj", "👍👍", "", false},
		{"lone regional indicator", "🇫", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := AssetID(tt.cluster)
			if ok != tt.wantOK || id != tt.wantID {
				t.Fatalf("AssetID(%q) = %q, %v, want %q, %v", tt.cluster, id, ok, tt.wantID, tt.wantOK)
			}
			if got := IsCluster(tt.cluster); got != tt.wantOK {
				t.Fatalf("IsCluster(%q) = %v, want %v", tt.cluster, got, tt.wantOK)
			}
		})
	}
}
