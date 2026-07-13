package common

// JSONCloseTracker tracks the brace depth of a JSON object streamed across
// multiple fragments, so a demasker can be flushed exactly when the object
// closes. Unlike a per-fragment scan, it carries string-literal and escape
// state ACROSS fragments: a value split mid-string (e.g. `{"k":"a` + `b}c"}`)
// keeps the interior '}' from being mistaken for a real close, which would
// otherwise flush prematurely and could split a placeholder straddling that
// boundary.
//
// The zero value is ready to use. Feed each fragment in arrival order.
//
// Used by the OpenAI chat-completions processor (tool_call arguments), the
// Anthropic Messages processor (tool_use input_json_delta) and the OpenAI
// Responses processor (function_call arguments).
type JSONCloseTracker struct {
	depth    int
	inString bool
	escaped  bool
	opened   bool // saw at least one '{' — guards against a stray leading '}'
}

// Feed consumes one fragment and reports whether the top-level JSON object
// just closed within it: the depth returned to 0 (after having opened) because
// of a '}' seen outside any string literal in this fragment. String and escape
// state persist across calls.
func (t *JSONCloseTracker) Feed(s string) bool {
	closedHere := false
	for i := 0; i < len(s); i++ {
		ch := s[i]

		if t.escaped {
			t.escaped = false
			continue
		}
		if ch == '\\' {
			t.escaped = true
			continue
		}
		if ch == '"' {
			t.inString = !t.inString
			continue
		}
		if t.inString {
			continue
		}

		switch ch {
		case '{':
			t.depth++
			t.opened = true
		case '}':
			t.depth--
			if t.opened && t.depth == 0 {
				closedHere = true
			}
		}
	}
	return closedHere
}
