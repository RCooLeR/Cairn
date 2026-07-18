package lineage

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type ParseOptions struct {
	BuildArgs map[string]string
	Target    string
}

type DockerfileParseResult struct {
	Stages          []DockerfileStage
	FinalStageIndex int
	Warnings        []string
	Errors          []string
	UnresolvedArgs  []string
}

type DockerfileStage struct {
	Index          int
	Name           string
	BaseRaw        string
	BaseResolved   string
	Platform       string
	Line           int
	Internal       bool
	Scratch        bool
	Unresolved     bool
	Pinned         bool
	BaseStageIndex int
}

type dockerfileLine struct {
	Number int
	Text   string
}

type argDef struct {
	Name       string
	Value      string
	HasDefault bool
}

type argValue struct {
	Value      string
	Unresolved []string
}

type heredocDelimiter struct {
	Value     string
	StripTabs bool
}

func ParseDockerfile(content string, opts ParseOptions) DockerfileParseResult {
	escape, directiveErrors := dockerfileEscape(content)
	args := automaticPlatformArgs(opts.BuildArgs)
	stageByName := map[string]int{}
	result := DockerfileParseResult{FinalStageIndex: -1, Errors: directiveErrors}
	lines, lineErrors := logicalDockerfileLines(content, escape)
	result.Errors = appendUnique(result.Errors, lineErrors...)
	seenFrom := false

	for _, line := range lines {
		fields := splitInstructionFields(line.Text, escape)
		if len(fields) == 0 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "ARG":
			// Only ARG instructions before the first FROM are in the global
			// scope used to interpolate FROM instructions. Stage-local ARGs
			// must not leak into later, unrelated stages.
			if !seenFrom {
				applyGlobalArgDefinitions(args, opts.BuildArgs, parseArgDefinitions(fields[1:]))
			}
		case "FROM":
			seenFrom = true
			baseRaw, platform, stageName, ok := parseFromFields(fields[1:])
			if !ok {
				result.Errors = append(result.Errors, "invalid FROM instruction on line "+strconv.Itoa(line.Number))
				continue
			}
			baseResolved, unresolved := substituteArgs(baseRaw, args)
			platformResolved, _ := substituteArgs(platform, args)
			unresolved = compactStrings(unresolved)
			stage := DockerfileStage{
				Index:          len(result.Stages),
				Name:           stageName,
				BaseRaw:        baseRaw,
				BaseResolved:   baseResolved,
				Platform:       platformResolved,
				Line:           line.Number,
				BaseStageIndex: -1,
				Unresolved:     len(unresolved) > 0,
				Pinned:         strings.Contains(baseResolved, "@sha256:"),
			}
			lowerBase := strings.ToLower(baseResolved)
			if lowerBase == "scratch" {
				stage.Scratch = true
			} else if prior, ok := stageByName[lowerBase]; ok {
				stage.Internal = true
				stage.BaseStageIndex = prior
			}
			result.Stages = append(result.Stages, stage)
			// Numeric stage references keep legacy multi-stage fixtures resolvable.
			stageByName[strconv.Itoa(stage.Index)] = stage.Index
			if stage.Name != "" {
				lowerName := strings.ToLower(stage.Name)
				if _, duplicate := stageByName[lowerName]; duplicate {
					result.Errors = append(result.Errors, fmt.Sprintf("duplicate Dockerfile stage name %q on line %d", stage.Name, line.Number))
				} else {
					stageByName[lowerName] = stage.Index
				}
			}
			result.UnresolvedArgs = appendUnique(result.UnresolvedArgs, unresolved...)
		}
	}

	if len(result.Stages) == 0 {
		result.Errors = appendUnique(result.Errors, "dockerfile has no valid FROM instruction")
	}
	if len(result.Errors) == 0 {
		finalIndex, err := resolveFinalStageIndex(result.Stages, opts.Target)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.FinalStageIndex = finalIndex
		}
	}
	return result
}

func (r DockerfileParseResult) FinalExternalStageIndex() int {
	if r.FinalStageIndex < 0 || r.FinalStageIndex >= len(r.Stages) {
		return -1
	}
	seen := map[int]struct{}{}
	index := r.FinalStageIndex
	for index >= 0 && index < len(r.Stages) {
		if _, ok := seen[index]; ok {
			return -1
		}
		seen[index] = struct{}{}
		stage := r.Stages[index]
		if stage.Scratch {
			return -1
		}
		if !stage.Internal {
			return index
		}
		index = stage.BaseStageIndex
	}
	return -1
}

func normalizeDockerfileContent(content string) string {
	content = strings.TrimPrefix(content, "\ufeff")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.ReplaceAll(content, "\r", "\n")
}

func dockerfileEscape(content string) (rune, []string) {
	escape := rune('\\')
	errors := []string{}
	seen := map[string]struct{}{}
	for index, raw := range strings.Split(normalizeDockerfileContent(content), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || !strings.HasPrefix(trimmed, "#") {
			break
		}
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		key, value, ok := strings.Cut(body, "=")
		if !ok {
			// A regular comment ends the parser-directive preamble.
			break
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "syntax" && key != "escape" && key != "check" {
			// Unknown directives are comments and also end the preamble.
			break
		}
		if _, duplicate := seen[key]; duplicate {
			errors = append(errors, fmt.Sprintf("duplicate Dockerfile parser directive %q on line %d", key, index+1))
			continue
		}
		seen[key] = struct{}{}
		if key != "escape" {
			continue
		}
		switch value {
		case `\`:
			escape = '\\'
		case "`":
			escape = '`'
		default:
			errors = append(errors, fmt.Sprintf("invalid Dockerfile escape directive %q on line %d", value, index+1))
		}
	}
	return escape, errors
}

func automaticPlatformArgs(buildArgs map[string]string) map[string]argValue {
	result := map[string]argValue{}
	for _, name := range []string{
		"BUILDPLATFORM", "BUILDOS", "BUILDARCH", "BUILDVARIANT",
		"TARGETPLATFORM", "TARGETOS", "TARGETARCH", "TARGETVARIANT",
	} {
		if value, ok := buildArgs[name]; ok {
			result[name] = argValue{Value: value}
		}
	}
	return result
}

func applyGlobalArgDefinitions(args map[string]argValue, buildArgs map[string]string, defs []argDef) {
	for _, def := range defs {
		if override, ok := buildArgs[def.Name]; ok {
			args[def.Name] = argValue{Value: override}
			continue
		}
		if !def.HasDefault {
			// A declaration without a default preserves an earlier value. This
			// also matches consuming a predefined global platform argument.
			continue
		}
		value, unresolved := substituteArgs(def.Value, args)
		args[def.Name] = argValue{Value: value, Unresolved: compactStrings(unresolved)}
	}
}

func hasContinuation(line string, escape rune) bool {
	if line == "" {
		return false
	}
	count := 0
	for index := len(line); index > 0; {
		ch := rune(line[index-1])
		if ch != escape {
			break
		}
		count++
		index--
	}
	return count%2 == 1
}

func dockerfileHeredocDelimiters(input string, escape rune) []heredocDelimiter {
	delimiters := []heredocDelimiter{}
	for index := 0; index < len(input); {
		ch := rune(input[index])
		if ch == escape {
			index += 2
			continue
		}
		if ch == '\'' || ch == '"' {
			quote := byte(ch)
			index++
			for index < len(input) {
				if rune(input[index]) == escape && index+1 < len(input) {
					index += 2
					continue
				}
				if input[index] == quote {
					index++
					break
				}
				index++
			}
			continue
		}
		if index+1 >= len(input) || input[index] != '<' || input[index+1] != '<' {
			index++
			continue
		}
		index += 2
		if index < len(input) && input[index] == '<' {
			// A shell here-string is not a Dockerfile heredoc declaration.
			index++
			continue
		}
		stripTabs := false
		if index < len(input) && input[index] == '-' {
			stripTabs = true
			index++
		}
		for index < len(input) && unicode.IsSpace(rune(input[index])) {
			index++
		}
		var builder strings.Builder
		if index < len(input) && (input[index] == '\'' || input[index] == '"') {
			quote := input[index]
			index++
			for index < len(input) && input[index] != quote {
				builder.WriteByte(input[index])
				index++
			}
			if index < len(input) {
				index++
			}
		} else {
			for index < len(input) && !unicode.IsSpace(rune(input[index])) {
				if rune(input[index]) == escape && index+1 < len(input) {
					index++
				}
				builder.WriteByte(input[index])
				index++
			}
		}
		if builder.Len() > 0 {
			delimiters = append(delimiters, heredocDelimiter{Value: builder.String(), StripTabs: stripTabs})
		}
	}
	return delimiters
}

func logicalDockerfileLines(content string, escape rune) ([]dockerfileLine, []string) {
	content = normalizeDockerfileContent(content)
	physical := strings.Split(content, "\n")
	lines := []dockerfileLine{}
	errors := []string{}
	for index := 0; index < len(physical); {
		var builder strings.Builder
		startLine := 0
		continuing := false
		for index < len(physical) {
			raw := physical[index]
			lineNo := index + 1
			index++
			line := strings.TrimRight(raw, " \t")
			// Docker removes full-line comments before processing continuations.
			// A # elsewhere is an instruction argument, not an inline comment.
			if strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
				continue
			}
			if builder.Len() == 0 {
				startLine = lineNo
			}
			trimmed := strings.TrimSpace(line)
			continued := hasContinuation(trimmed, escape)
			if continued {
				trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, string(escape)))
			}
			if builder.Len() > 0 && trimmed != "" {
				builder.WriteByte(' ')
			}
			builder.WriteString(trimmed)
			continuing = continued
			if continuing {
				continue
			}
			break
		}

		text := strings.TrimSpace(builder.String())
		if text == "" {
			continue
		}
		lines = append(lines, dockerfileLine{Number: startLine, Text: text})
		for _, delimiter := range dockerfileHeredocDelimiters(text, escape) {
			closed := false
			for index < len(physical) {
				candidate := physical[index]
				index++
				if delimiter.StripTabs {
					candidate = strings.TrimLeft(candidate, "\t")
				}
				if candidate == delimiter.Value {
					closed = true
					break
				}
			}
			if !closed {
				errors = append(errors, fmt.Sprintf("unterminated Dockerfile heredoc %q opened on line %d", delimiter.Value, startLine))
				break
			}
		}
	}
	return lines, errors
}

func splitInstructionFields(input string, escape rune) []string {
	fields := []string{}
	var builder strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		fields = append(fields, builder.String())
		builder.Reset()
	}
	for _, ch := range input {
		if escaped {
			builder.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == escape {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				builder.WriteRune(ch)
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if unicode.IsSpace(ch) {
			flush()
			continue
		}
		builder.WriteRune(ch)
	}
	if escaped {
		builder.WriteRune(escape)
	}
	flush()
	return fields
}

func parseArgDefinitions(fields []string) []argDef {
	defs := []argDef{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		name, value, hasDefault := strings.Cut(field, "=")
		name = strings.TrimSpace(name)
		if !validArgName(name) {
			continue
		}
		defs = append(defs, argDef{
			Name:       name,
			Value:      value,
			HasDefault: hasDefault,
		})
	}
	return defs
}

func parseFromFields(fields []string) (base string, platform string, stageName string, ok bool) {
	index := 0
	for index < len(fields) {
		field := fields[index]
		if !strings.HasPrefix(field, "--") {
			break
		}
		if value, found := strings.CutPrefix(field, "--platform="); found {
			if value == "" {
				return "", "", "", false
			}
			platform = value
		} else if field == "--platform" && index+1 < len(fields) {
			index++
			platform = fields[index]
			if platform == "" {
				return "", "", "", false
			}
		} else {
			return "", "", "", false
		}
		index++
	}
	if index >= len(fields) {
		return "", platform, "", false
	}
	base = fields[index]
	index++
	if index == len(fields) {
		return base, platform, "", base != ""
	}
	if index+2 != len(fields) || !strings.EqualFold(fields[index], "AS") || fields[index+1] == "" {
		return "", platform, "", false
	}
	return base, platform, fields[index+1], base != ""
}

func substituteArgs(input string, args map[string]argValue) (string, []string) {
	var builder strings.Builder
	unresolved := []string{}
	for index := 0; index < len(input); {
		if input[index] != '$' {
			builder.WriteByte(input[index])
			index++
			continue
		}
		if index+1 >= len(input) {
			builder.WriteByte(input[index])
			index++
			continue
		}
		if input[index+1] == '{' {
			end := strings.IndexByte(input[index+2:], '}')
			if end < 0 {
				builder.WriteByte(input[index])
				index++
				continue
			}
			name := input[index+2 : index+2+end]
			token := input[index : index+3+end]
			if value, ok := args[name]; ok {
				builder.WriteString(value.Value)
				unresolved = append(unresolved, value.Unresolved...)
			} else {
				builder.WriteString(token)
				unresolved = append(unresolved, name)
			}
			index += len(token)
			continue
		}
		next := index + 1
		if !isArgStart(input[next]) {
			builder.WriteByte(input[index])
			index++
			continue
		}
		end := next + 1
		for end < len(input) && isArgPart(input[end]) {
			end++
		}
		name := input[next:end]
		token := input[index:end]
		if value, ok := args[name]; ok {
			builder.WriteString(value.Value)
			unresolved = append(unresolved, value.Unresolved...)
		} else {
			builder.WriteString(token)
			unresolved = append(unresolved, name)
		}
		index = end
	}
	return builder.String(), unresolved
}

func resolveFinalStageIndex(stages []DockerfileStage, target string) (int, error) {
	if len(stages) == 0 {
		return -1, fmt.Errorf("cannot resolve a build target without Dockerfile stages")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return len(stages) - 1, nil
	}
	if numeric, err := strconv.Atoi(target); err == nil {
		if numeric < 0 || numeric >= len(stages) {
			return -1, fmt.Errorf("dockerfile build target index %d is out of range for %d stages", numeric, len(stages))
		}
		return numeric, nil
	}
	for _, stage := range stages {
		if strings.EqualFold(stage.Name, target) {
			return stage.Index, nil
		}
	}
	return -1, fmt.Errorf("dockerfile build target %q does not match a named stage", target)
}

func appendUnique(values []string, next ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(next))
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range next {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		values = append(values, value)
		seen[value] = struct{}{}
	}
	return values
}

func compactStrings(values []string) []string {
	return appendUnique(nil, values...)
}

func validArgName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		if index == 0 && !isArgStart(name[index]) {
			return false
		}
		if index > 0 && !isArgPart(name[index]) {
			return false
		}
	}
	return true
}

func isArgStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isArgPart(ch byte) bool {
	return isArgStart(ch) || ch >= '0' && ch <= '9'
}
