package common

import "testing"

// feedAll feeds fragments in order and returns the index at which the tracker
// first reported a close, or -1 if it never did.
func feedAll(fragments []string) int {
	var t JSONCloseTracker
	closedAt := -1
	for i, f := range fragments {
		if t.Feed(f) && closedAt == -1 {
			closedAt = i
		}
	}
	return closedAt
}

func TestJSONCloseTracker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fragments []string
		wantClose int // index of the fragment that closes the object, -1 if none
	}{
		{"complete object in one fragment", []string{`{"x":"y"}`}, 0},
		{"empty object", []string{`{}`}, 0},
		{"split across fragments", []string{`{"city":`, `"Paris"`, `}`}, 2},
		{
			// The regression case: a value containing '}' with the closing
			// quote+brace in a later fragment. Fragment 1 leaves the tracker
			// mid-string, so the in-string '}' of fragment 2 must NOT close it.
			name:      "in-string brace does not close early",
			fragments: []string{`{"note":"start`, `end } more`, `"}`},
			wantClose: 2,
		},
		{
			// Escaped quote inside the value must not end the string, so the
			// following '}' stays in-string until the real close.
			name:      "escaped quote keeps string open",
			fragments: []string{`{"k":"a\`, `" }`, `"}`},
			wantClose: 2,
		},
		{"nested objects", []string{`{"a":{"b":1}`, `,"c":2}`}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := feedAll(tt.fragments); got != tt.wantClose {
				t.Fatalf("close index = %d, want %d (fragments %q)", got, tt.wantClose, tt.fragments)
			}
		})
	}
}

// A stray closing brace with no prior '{' must not report a close.
func TestJSONCloseTracker_NoSpuriousCloseWithoutOpen(t *testing.T) {
	t.Parallel()
	var trk JSONCloseTracker
	if trk.Feed(`}`) {
		t.Fatal("closing brace without a prior open should not report a close")
	}
}
