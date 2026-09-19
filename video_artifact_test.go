package main

import (
	"net/url"
	"testing"
)

// Non-minimax_v3 backends hand back a finished CDN URL, but the platform shape
// this service serves has only one artifact slot — `file_id` — which travels to
// /v1/files/retrieve as a **query parameter**. So the URL has to survive a
// percent-encode round trip.
//
// The failure this guards against is silent and nasty: a signed CDN link
// contains `&`, and if the value were put in the query string unescaped, the
// caller's parser would truncate it at the first `&`. The request would still
// return HTTP 200 and a plausible-looking URL — just one that 403s on download.
func TestMediaURLRoundTrip(t *testing.T) {
	cases := []string{
		"https://cdn.hailuoai.video/moss/prod/2026-09-19-09/video/1789782289066058013-557532212307435522.mp4",
		// The case that matters: multiple `&`-separated signature params.
		"https://cdn.example.com/v.mp4?token=abc&expires=123&sig=xy",
		"https://cdn.example.com/a b/c.mp4", // literal space
		"https://cdn.example.com/plus+sign.mp4",
		"https://cdn.example.com/hash#frag.mp4",
	}

	for _, raw := range cases {
		encoded := encodeMediaURL(raw)

		// Simulate the hop through a query string: the caller builds
		// `?file_id=<encoded>` and the server parses it back out.
		q := url.Values{}
		q.Set("file_id", encoded)
		received := q.Get("file_id")

		got, ok := decodeMediaURL(received)
		if !ok {
			t.Fatalf("decodeMediaURL(%q) did not recognise a URL", received)
		}
		if got != raw {
			t.Fatalf("round trip lost data:\n  in   %q\n  out  %q", raw, got)
		}
	}
}

// A genuine hub file id must NOT be mistaken for a URL, or every minimax_v3
// task would skip the /v1/files/retrieve exchange and hand back a bare id.
func TestDecodeMediaURLRejectsFileID(t *testing.T) {
	for _, id := range []string{"L8ONzGzOpqzD", "3y9Jk3qdDonA", ""} {
		if got, ok := decodeMediaURL(id); ok {
			t.Fatalf("decodeMediaURL(%q) = %q, want not-a-URL", id, got)
		}
	}
}
