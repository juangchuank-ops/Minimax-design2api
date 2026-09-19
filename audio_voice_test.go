package main

import "testing"

// resolveVoice has two jobs: translate the eleven fixed OpenAI voice names, and
// let every real MiniMax `voice_id` through untouched. The second half is what
// keeps the 671-entry catalogue reachable, so a stray `strings.ToLower` on the
// fall-through path would silently break it — hub ids are case-sensitive.
func TestResolveVoice(t *testing.T) {
	t.Run("OpenAI names translate", func(t *testing.T) {
		for name := range openAIVoiceAliases {
			got := resolveVoice(name)
			if got == name {
				t.Errorf("resolveVoice(%q) returned the input; alias not applied", name)
			}
			if got != openAIVoiceAliases[name] {
				t.Errorf("resolveVoice(%q) = %q, want %q", name, got, openAIVoiceAliases[name])
			}
		}
	})

	t.Run("OpenAI names are case-insensitive", func(t *testing.T) {
		for name := range openAIVoiceAliases {
			upper := resolveVoice(upperFirst(name))
			if upper != openAIVoiceAliases[name] {
				t.Errorf("resolveVoice(%q) = %q, want %q",
					upperFirst(name), upper, openAIVoiceAliases[name])
			}
		}
	})

	t.Run("catalogue ids pass through verbatim", func(t *testing.T) {
		// Mixed case and punctuation are load-bearing: the hub really does spell
		// these this way, and lowercasing them yields code=2054.
		for _, id := range []string{
			"English_CalmWoman",
			"English_Gentle-voiced_man",
			"English_Deep-tonedMan",
			"Chinese_wenrounvxing",
			"Cantonese_crisp_news_anchor_vv2",
		} {
			if got := resolveVoice(id); got != id {
				t.Errorf("resolveVoice(%q) = %q, want it unchanged", id, got)
			}
		}
	})

	t.Run("empty falls back to the default", func(t *testing.T) {
		if got := resolveVoice(""); got != defaultVoice {
			t.Errorf("resolveVoice(\"\") = %q, want %q", got, defaultVoice)
		}
		if got := resolveVoice("   "); got != defaultVoice {
			t.Errorf("resolveVoice(blank) = %q, want %q", got, defaultVoice)
		}
	})

	t.Run("every alias target is unique-ish and non-empty", func(t *testing.T) {
		// Not a catalogue check (that needs the network — see
		// tools/check_voices.py), just a guard against an empty value sneaking in.
		for name, target := range openAIVoiceAliases {
			if target == "" {
				t.Errorf("alias %q maps to an empty voice id", name)
			}
		}
		if defaultVoice == "" {
			t.Error("defaultVoice is empty")
		}
	})
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}
