package registry

import "strings"

type authChallenge struct {
	Scheme string
	Params map[string]string
}

func parseWWWAuthenticate(value string) authChallenge {
	value = strings.TrimSpace(value)
	if value == "" {
		return authChallenge{Params: map[string]string{}}
	}
	scheme, rest, ok := strings.Cut(value, " ")
	if !ok {
		return authChallenge{Scheme: value, Params: map[string]string{}}
	}
	return authChallenge{Scheme: scheme, Params: parseChallengeParams(rest)}
}

func parseChallengeParams(value string) map[string]string {
	params := map[string]string{}
	for _, part := range splitChallengeParams(value) {
		key, raw, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		raw = strings.TrimSpace(raw)
		if strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) && len(raw) >= 2 {
			raw = strings.TrimSuffix(strings.TrimPrefix(raw, `"`), `"`)
			raw = strings.ReplaceAll(raw, `\"`, `"`)
		}
		if key != "" {
			params[key] = raw
		}
	}
	return params
}

func splitChallengeParams(value string) []string {
	parts := []string{}
	var current strings.Builder
	inQuote := false
	escaped := false
	for _, r := range value {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			current.WriteRune(r)
			escaped = true
		case r == '"':
			inQuote = !inQuote
			current.WriteRune(r)
		case r == ',' && !inQuote:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}
