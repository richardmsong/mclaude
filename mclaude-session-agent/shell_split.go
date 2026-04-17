package main

import "fmt"

// shellSplit splits a string into tokens using POSIX-like shell quoting rules:
//   - Tokens are separated by whitespace.
//   - Single-quoted strings: 'foo bar' → "foo bar" (no escape processing inside).
//   - Double-quoted strings: "foo bar" → "foo bar", with \" → " and \\ → \ escapes.
//   - Backslash outside quotes is not treated as an escape (only handled inside
//     double-quoted strings).
//   - Returns an error for unclosed quotes.
//
// Example:
//
//	shellSplit(`--disallowedTools "Edit(src/**)" --model claude-opus-4-7`)
//	→ ["--disallowedTools", "Edit(src/**)", "--model", "claude-opus-4-7"], nil
func shellSplit(s string) ([]string, error) {
	var tokens []string
	var cur []byte
	inToken := false

	i := 0
	for i < len(s) {
		ch := s[i]

		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			// Whitespace: end the current token if one is in progress.
			if inToken {
				tokens = append(tokens, string(cur))
				cur = cur[:0]
				inToken = false
			}
			i++

		case ch == '\'':
			// Single-quoted string: consume until the closing '.
			inToken = true
			i++ // skip opening quote
			for i < len(s) && s[i] != '\'' {
				cur = append(cur, s[i])
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("shellSplit: unclosed single quote in %q", s)
			}
			i++ // skip closing quote

		case ch == '"':
			// Double-quoted string: consume until the closing ", processing \" and \\.
			inToken = true
			i++ // skip opening quote
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					next := s[i+1]
					if next == '"' || next == '\\' {
						cur = append(cur, next)
						i += 2
						continue
					}
				}
				cur = append(cur, s[i])
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("shellSplit: unclosed double quote in %q", s)
			}
			i++ // skip closing quote

		default:
			// Regular character: append to current token.
			inToken = true
			cur = append(cur, ch)
			i++
		}
	}

	// Flush any remaining token.
	if inToken {
		tokens = append(tokens, string(cur))
	}

	return tokens, nil
}
