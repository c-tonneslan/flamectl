package main

import "testing"

func TestClampSampleIndex(t *testing.T) {
	cases := []struct {
		name         string
		idx, n       int
		wantResolved int
		wantOK       bool
	}{
		{"zero", 0, 3, 0, true},
		{"in range", 1, 3, 1, true},
		{"last valid", 2, 3, 2, true},
		{"equal to length", 3, 3, 0, false},
		{"too large", 5, 3, 0, false},
		{"negative", -1, 3, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := clampSampleIndex(c.idx, c.n)
			if got != c.wantResolved || ok != c.wantOK {
				t.Errorf("clampSampleIndex(%d, %d) = (%d, %t), want (%d, %t)",
					c.idx, c.n, got, ok, c.wantResolved, c.wantOK)
			}
		})
	}
}
