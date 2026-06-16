package lineage

import (
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

func ParseDockerfile(content string, opts ParseOptions) DockerfileParseResult {
	args := copyStringMap(opts.BuildArgs)
	stageByName := map[string]int{}
	result := DockerfileParseResult{FinalStageIndex: -1}

	for _, line := range logicalDockerfileLines(content) {
		fields := splitInstructionFields(line.Text)
		if len(fields) == 0 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "ARG":
			for _, def := range parseArgDefinitions(fields[1:]) {
				if _, ok := args[def.Name]; !ok && def.HasDefault {
					value, unresolved := substituteArgs(def.Value, args)
					args[def.Name] = value
					result.UnresolvedArgs = appendUnique(result.UnresolvedArgs, unresolved...)
				}
			}
		case "FROM":
			baseRaw, platform, stageName, ok := parseFromFields(fields[1:])
			if !ok {
				result.Warnings = append(result.Warnings, "invalid FROM instruction on line "+strconv.Itoa(line.Number))
				continue
			}
			baseResolved, unresolved := substituteArgs(baseRaw, args)
			unresolved = compactStrings(unresolved)
			stage := DockerfileStage{
				Index:          len(result.Stages),
				Name:           stageName,
				BaseRaw:        baseRaw,
				BaseResolved:   baseResolved,
				Platform:       platform,
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
				stageByName[strings.ToLower(stage.Name)] = stage.Index
			}
			result.UnresolvedArgs = appendUnique(result.UnresolvedArgs, unresolved...)
		}
	}

	result.FinalStageIndex = resolveFinalStageIndex(result.Stages, opts.Target)
	if len(result.Stages) == 0 {
		result.Warnings = append(result.Warnings, "dockerfile has no FROM instruction")
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

func logicalDockerfileLines(content string) []dockerfileLine {
	content = strings.TrimPrefix(content, "\ufeff")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	physical := strings.Split(content, "\n")
	lines := []dockerfileLine{}
	var builder strings.Builder
	startLine := 0
	continuing := false
	for index, raw := range physical {
		lineNo := index + 1
		line := strings.TrimRight(raw, " \t")
		if !continuing && strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		if !continuing {
			startLine = lineNo
		}
		trimmed := strings.TrimSpace(line)
		continued := strings.HasSuffix(trimmed, `\`)
		if continued {
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, `\`))
		}
		if builder.Len() > 0 && trimmed != "" {
			builder.WriteByte(' ')
		}
		builder.WriteString(trimmed)
		continuing = continued
		if continuing {
			continue
		}
		text := strings.TrimSpace(stripInlineComment(builder.String()))
		if text != "" {
			lines = append(lines, dockerfileLine{Number: startLine, Text: text})
		}
		builder.Reset()
	}
	if builder.Len() > 0 {
		text := strings.TrimSpace(stripInlineComment(builder.String()))
		if text != "" {
			lines = append(lines, dockerfileLine{Number: startLine, Text: text})
		}
	}
	return lines
}

func stripInlineComment(line string) string {
	var quote rune
	escaped := false
	for index, ch := range line {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == '#' && (index == 0 || unicode.IsSpace(rune(line[index-1]))) {
			return line[:index]
		}
	}
	return line
}

func splitInstructionFields(input string) []string {
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
		if ch == '\\' {
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
		builder.WriteRune('\\')
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
			platform = value
		} else if field == "--platform" && index+1 < len(fields) {
			index++
			platform = fields[index]
		}
		index++
	}
	if index >= len(fields) {
		return "", platform, "", false
	}
	base = fields[index]
	index++
	if index+1 < len(fields) && strings.EqualFold(fields[index], "AS") {
		stageName = fields[index+1]
	}
	return base, platform, stageName, base != ""
}

func substituteArgs(input string, args map[string]string) (string, []string) {
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
				builder.WriteString(value)
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
			builder.WriteString(value)
		} else {
			builder.WriteString(token)
			unresolved = append(unresolved, name)
		}
		index = end
	}
	return builder.String(), unresolved
}

func resolveFinalStageIndex(stages []DockerfileStage, target string) int {
	if len(stages) == 0 {
		return -1
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return len(stages) - 1
	}
	if numeric, err := strconv.Atoi(target); err == nil && numeric >= 0 && numeric < len(stages) {
		return numeric
	}
	for _, stage := range stages {
		if strings.EqualFold(stage.Name, target) {
			return stage.Index
		}
	}
	return len(stages) - 1
}

func copyStringMap(values map[string]string) map[string]string {
	copied := map[string]string{}
	for key, value := range values {
		copied[key] = value
	}
	return copied
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
