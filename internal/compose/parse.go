package compose

import (
	"bufio"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/RCooLeR/Cairn/internal/models"
	"gopkg.in/yaml.v3"
)

var versionPattern = regexp.MustCompile(`v?([0-9]+(?:\.[0-9]+){1,2})`)

func ParseVersionJSON(raw string) (*Version, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("compose version output is empty")
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(trimmed), &doc); err == nil {
		version := normalizeVersion(firstString(doc, "version", "Version"))
		if version == "" {
			return nil, fmt.Errorf("compose version JSON did not include a version")
		}
		return &Version{
			Version:   version,
			GitCommit: firstString(doc, "gitCommit", "GitCommit", "git_commit"),
		}, nil
	}

	match := versionPattern.FindStringSubmatch(trimmed)
	if len(match) < 2 {
		return nil, fmt.Errorf("compose version output did not include a version")
	}
	return &Version{Version: normalizeVersion(match[1])}, nil
}

func VersionAtLeast(version string, minimum string) bool {
	got := versionParts(version)
	want := versionParts(minimum)
	for i := range want {
		if got[i] > want[i] {
			return true
		}
		if got[i] < want[i] {
			return false
		}
	}
	return true
}

func ParseProjectsJSON(raw string) ([]Project, error) {
	records, err := decodeJSONRecords(raw)
	if err != nil {
		return nil, err
	}
	projects := make([]Project, 0, len(records))
	for _, record := range records {
		name := firstString(record, "Name", "name")
		if name == "" {
			continue
		}
		projects = append(projects, Project{
			Name:        name,
			Status:      firstString(record, "Status", "status"),
			ConfigFiles: stringListField(record, "ConfigFiles", "configFiles", "ConfigFile", "config_files"),
		})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects, nil
}

func ParsePSJSON(raw string) ([]ContainerStatus, error) {
	records, err := decodeJSONRecords(raw)
	if err != nil {
		return nil, err
	}
	containers := make([]ContainerStatus, 0, len(records))
	for _, record := range records {
		container := ContainerStatus{
			ID:       firstString(record, "ID", "Id", "id"),
			Name:     firstString(record, "Name", "name"),
			Project:  firstString(record, "Project", "project"),
			Service:  firstString(record, "Service", "service"),
			State:    firstString(record, "State", "state"),
			Health:   firstString(record, "Health", "health"),
			ExitCode: intField(record, "ExitCode", "exitCode", "exit_code"),
		}
		if rawPublishers, ok := firstField(record, "Publishers", "publishers"); ok {
			container.Publishers = parsePublishers(rawPublishers)
		}
		containers = append(containers, container)
	}
	sort.Slice(containers, func(i, j int) bool {
		if containers[i].Service == containers[j].Service {
			return containers[i].Name < containers[j].Name
		}
		return containers[i].Service < containers[j].Service
	})
	return containers, nil
}

func ServiceStatuses(containers []ContainerStatus) []models.ComposeServiceStatus {
	type aggregate struct {
		name       string
		total      int
		running    int
		states     []string
		health     []string
		ports      []models.PortBinding
		portsByKey map[string]struct{}
	}

	byService := map[string]*aggregate{}
	for _, container := range containers {
		service := container.Service
		if service == "" {
			service = container.Name
		}
		if service == "" {
			continue
		}
		agg := byService[service]
		if agg == nil {
			agg = &aggregate{name: service, portsByKey: map[string]struct{}{}}
			byService[service] = agg
		}
		agg.total++
		if strings.EqualFold(container.State, "running") {
			agg.running++
		}
		agg.states = append(agg.states, container.State)
		agg.health = append(agg.health, container.Health)
		for _, publisher := range container.Publishers {
			protocol := publisher.Protocol
			if protocol == "" {
				protocol = "tcp"
			}
			binding := models.PortBinding{
				HostIP:        publisher.HostIP,
				HostPort:      publisher.PublishedPort,
				ContainerPort: publisher.TargetPort,
				Protocol:      protocol,
			}
			key := binding.HostIP + "|" + binding.HostPort + "|" + binding.ContainerPort + "|" + binding.Protocol
			if binding.ContainerPort == "" {
				continue
			}
			if _, exists := agg.portsByKey[key]; exists {
				continue
			}
			agg.portsByKey[key] = struct{}{}
			agg.ports = append(agg.ports, binding)
		}
	}

	names := make([]string, 0, len(byService))
	for name := range byService {
		names = append(names, name)
	}
	sort.Strings(names)
	statuses := make([]models.ComposeServiceStatus, 0, len(names))
	for _, name := range names {
		agg := byService[name]
		sort.Slice(agg.ports, func(i, j int) bool {
			if agg.ports[i].ContainerPort == agg.ports[j].ContainerPort {
				return agg.ports[i].HostPort < agg.ports[j].HostPort
			}
			return agg.ports[i].ContainerPort < agg.ports[j].ContainerPort
		})
		statuses = append(statuses, models.ComposeServiceStatus{
			Name:     name,
			Replicas: agg.total,
			Running:  agg.running,
			Status:   aggregateStatus(agg.running, agg.total, agg.states),
			Health:   aggregateHealth(agg.health),
			Ports:    agg.ports,
		})
	}
	return statuses
}

func ParseConfigYAML(raw string) (*ConfigResult, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return &ConfigResult{Raw: raw, Valid: false, Errors: []string{err.Error()}}, err
	}

	servicesDoc, ok := asMap(doc["services"])
	if !ok {
		return &ConfigResult{Raw: raw, Valid: false, Errors: []string{"compose config did not include services"}}, fmt.Errorf("compose config did not include services")
	}

	serviceNames := make([]string, 0, len(servicesDoc))
	for name := range servicesDoc {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	envSet := map[string]struct{}{}
	services := make([]ServiceConfig, 0, len(serviceNames))
	for _, name := range serviceNames {
		serviceDoc, ok := asMap(servicesDoc[name])
		if !ok {
			continue
		}
		service := ServiceConfig{
			Name:      name,
			Image:     stringValue(serviceDoc["image"]),
			Ports:     stringSlice(serviceDoc["ports"]),
			DependsOn: dependencyNames(serviceDoc["depends_on"]),
			EnvFiles:  envFiles(serviceDoc["env_file"]),
			Profiles:  stringSlice(serviceDoc["profiles"]),
		}
		if _, exists := serviceDoc["healthcheck"]; exists {
			service.HasHealthcheck = true
		}
		parseBuild(serviceDoc["build"], &service)
		for _, envFile := range service.EnvFiles {
			envSet[envFile] = struct{}{}
		}
		services = append(services, service)
	}

	envFiles := make([]string, 0, len(envSet))
	for envFile := range envSet {
		envFiles = append(envFiles, envFile)
	}
	sort.Strings(envFiles)

	return &ConfigResult{
		Raw:      raw,
		Services: services,
		EnvFiles: envFiles,
		Valid:    true,
		API: models.ComposeConfigResult{
			ResolvedYAML: raw,
			EnvFiles:     envFiles,
			Valid:        true,
		},
	}, nil
}

func decodeJSONRecords(raw string) ([]map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []map[string]any{}, nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var records []map[string]any
		if err := json.Unmarshal([]byte(trimmed), &records); err != nil {
			return nil, err
		}
		return records, nil
	}

	if strings.HasPrefix(trimmed, "{") && !strings.Contains(trimmed, "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(trimmed), &record); err != nil {
			return nil, err
		}
		return []map[string]any{record}, nil
	}

	var records []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func parsePublishers(value any) []Publisher {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	publishers := make([]Publisher, 0, len(values))
	for _, item := range values {
		doc, ok := asMap(item)
		if !ok {
			continue
		}
		publishers = append(publishers, Publisher{
			HostIP:        firstString(doc, "URL", "HostIP", "hostIP", "url"),
			TargetPort:    firstString(doc, "TargetPort", "targetPort", "target_port"),
			PublishedPort: firstString(doc, "PublishedPort", "publishedPort", "published_port"),
			Protocol:      firstString(doc, "Protocol", "protocol"),
		})
	}
	return publishers
}

func parseBuild(value any, service *ServiceConfig) {
	switch typed := value.(type) {
	case string:
		service.BuildContext = typed
	case map[string]any:
		parseBuildMap(typed, service)
	case map[any]any:
		if mapped, ok := asMap(typed); ok {
			parseBuildMap(mapped, service)
		}
	}
}

func parseBuildMap(doc map[string]any, service *ServiceConfig) {
	service.BuildContext = stringValue(doc["context"])
	service.DockerfilePath = stringValue(doc["dockerfile"])
	service.BuildTarget = stringValue(doc["target"])
	service.BuildArgs = buildArgs(doc["args"])
}

func buildArgs(value any) map[string]string {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]string, len(typed))
		for key, value := range typed {
			out[key] = stringValue(value)
		}
		return out
	case []any:
		out := make(map[string]string, len(typed))
		for _, item := range typed {
			key, value, ok := strings.Cut(stringValue(item), "=")
			if !ok {
				key = stringValue(item)
				value = ""
			}
			if key != "" {
				out[key] = value
			}
		}
		return out
	default:
		return nil
	}
}

func dependencyNames(value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		names := make([]string, 0, len(typed))
		for name := range typed {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	case []any:
		return stringSlice(typed)
	default:
		return nil
	}
}

func envFiles(value any) []string {
	switch typed := value.(type) {
	case []any:
		files := make([]string, 0, len(typed))
		for _, item := range typed {
			if doc, ok := asMap(item); ok {
				if path := stringValue(doc["path"]); path != "" {
					files = append(files, path)
				}
				continue
			}
			if file := stringValue(item); file != "" {
				files = append(files, file)
			}
		}
		return uniqueSorted(files)
	default:
		if file := stringValue(value); file != "" {
			return []string{file}
		}
		return nil
	}
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if doc, ok := asMap(item); ok {
				out = append(out, stableMapString(doc))
				continue
			}
			if value := stringValue(item); value != "" {
				out = append(out, value)
			}
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		if value := stringValue(value); value != "" {
			return []string{value}
		}
		return nil
	}
}

func stringListField(record map[string]any, keys ...string) []string {
	value, ok := firstField(record, keys...)
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []any:
		return stringSlice(typed)
	default:
		raw := stringValue(typed)
		if raw == "" {
			return nil
		}
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	}
}

func firstString(record map[string]any, keys ...string) string {
	value, ok := firstField(record, keys...)
	if !ok {
		return ""
	}
	return stringValue(value)
}

func firstField(record map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func intField(record map[string]any, keys ...string) int {
	value, ok := firstField(record, keys...)
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		number, _ := typed.Int64()
		return int(number)
	default:
		number, _ := strconv.Atoi(stringValue(value))
		return number
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func asMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[stringValue(key)] = value
		}
		return out, true
	default:
		return nil, false
	}
}

func stableMapString(doc map[string]any) string {
	keys := make([]string, 0, len(doc))
	for key := range doc {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+stringValue(doc[key]))
	}
	return strings.Join(parts, ",")
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func aggregateStatus(running int, total int, states []string) models.ProjectStatus {
	for _, state := range states {
		lower := strings.ToLower(state)
		if strings.Contains(lower, "dead") || strings.Contains(lower, "error") {
			return models.ProjectStatusError
		}
	}
	switch {
	case total == 0:
		return models.ProjectStatusUnknown
	case running == total:
		return models.ProjectStatusRunning
	case running == 0:
		return models.ProjectStatusStopped
	default:
		return models.ProjectStatusPartial
	}
}

func aggregateHealth(values []string) models.HealthStatus {
	healthy := false
	for _, value := range values {
		switch strings.ToLower(value) {
		case "unhealthy":
			return models.HealthStatusUnhealthy
		case "starting":
			return models.HealthStatusStarting
		case "healthy":
			healthy = true
		}
	}
	if healthy {
		return models.HealthStatusHealthy
	}
	return models.HealthStatusUnknown
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimPrefix(version, "Docker Compose version ")
	if match := versionPattern.FindStringSubmatch(version); len(match) >= 2 {
		return match[1]
	}
	return version
}

func versionParts(version string) [3]int {
	version = normalizeVersion(version)
	parts := strings.Split(version, ".")
	var out [3]int
	for i := 0; i < len(out) && i < len(parts); i++ {
		number, _ := strconv.Atoi(numberPrefix(parts[i]))
		out[i] = number
	}
	return out
}

func numberPrefix(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r < '0' || r > '9' {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
