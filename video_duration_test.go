package main

import "testing"

// The duration ladders below are copied from `params.duration.options` in the
// live /api/v1/models/config response. If the hub ever widens or narrows one of
// them, this test is where the mismatch should surface — the clamp is the only
// thing standing between a caller's arbitrary number and an opaque upstream
// parameter error.
func TestClampVideoDuration(t *testing.T) {
	cases := []struct {
		name   string
		family string
		model  string
		in     int
		want   int
	}{
		// H3 line: 4-15 for the base model, 5-15 for Max/Turbo.
		{"h3 no preference", famMinimaxV3, "MiniMax-H3", 0, 0},
		{"h3 below min", famMinimaxV3, "MiniMax-H3", 1, 4},
		{"h3 in range", famMinimaxV3, "MiniMax-H3", 6, 6},
		{"h3 above max", famMinimaxV3, "MiniMax-H3", 99, 15},
		{"h3 max below min", famMinimaxV3, "MiniMax-H3-Max", 4, 5},
		{"h3 turbo in range", famMinimaxV3, "MiniMax-H3-Max-Turbo", 10, 10},

		// Wan 3.0: 2-30, the widest ladder of the set.
		{"wan3 no preference", famWan3, "wan3.0-video", 0, 0},
		{"wan3 below min", famWan3, "wan3.0-video", 1, 2},
		{"wan3 above max", famWan3, "wan3.0-video", 999, 30},

		// Veo3.1: exactly one legal value, so it is a fixed point rather than a
		// clamp. This is the case that actually bit us — new-api's hailuo
		// channel defaults unknown models to 6s.
		{"veo3 no preference", famVeo3, "veo-3.1-fast-generate-001", 0, 8},
		{"veo3 caller asked 6", famVeo3, "veo-3.1-fast-generate-001", 6, 8},
		{"veo3 caller asked 8", famVeo3, "veo-3.1-generate-001", 8, 8},
		{"veo3 caller asked 30", famVeo3, "veo-3.1-generate-001", 30, 8},

		// Kling: 3-15, shared by the plain, omni, avatar and motion backends.
		{"kling omni no preference", famKlingOmni, "kling-v3-omni", 0, 5}, // via klingDuration
		{"kling below min", famKling, "kling-v2", 2, 3},
		{"kling above max", famKling, "kling-v2", 60, 15},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got int
			if tc.family == famKlingOmni && tc.in == 0 {
				// klingDuration is the variant used on the wire.
				got = klingDuration(tc.family, tc.model, tc.in)
			} else {
				got = clampVideoDuration(tc.family, tc.model, tc.in)
			}
			if got != tc.want {
				t.Fatalf("clamp(%s, %s, %d) = %d, want %d",
					tc.family, tc.model, tc.in, got, tc.want)
			}
		})
	}
}

// A backend with no duration concept must stay untouched — sending a duration
// field to an endpoint that does not define one is how you get a 400.
func TestClampVideoDurationUnknownFamily(t *testing.T) {
	if got := clampVideoDuration("jimeng", "jimeng_motion_control", 6); got != 0 {
		t.Fatalf("jimeng duration = %d, want 0 (field must be omitted)", got)
	}
}
