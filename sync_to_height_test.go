package main

import "testing"

func TestSyncToHeightReached(t *testing.T) {
	tests := []struct {
		name   string
		height int32
		target int32
		want   bool
	}{
		{
			name:   "disabled at zero",
			height: 100,
			target: 0,
			want:   false,
		},
		{
			name:   "not reached",
			height: 99,
			target: 100,
			want:   false,
		},
		{
			name:   "reached",
			height: 100,
			target: 100,
			want:   true,
		},
		{
			name:   "passed",
			height: 101,
			target: 100,
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := syncToHeightReached(test.height, test.target)
			if got != test.want {
				t.Fatalf("syncToHeightReached(%d, %d) = %v, want %v",
					test.height, test.target, got, test.want)
			}
		})
	}
}
