package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	latestReleaseAPIURL  = "https://api.github.com/repos/Soju06/codex-lb/releases/latest"
	latestReleasePageURL = "https://github.com/Soju06/codex-lb/releases/latest"
	fallbackVersion      = "1.20.0-beta.3"
)

var versionPattern = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?$`)

type VersionResponse struct {
	CurrentVersion  string  `json:"currentVersion"`
	LatestVersion   *string `json:"latestVersion"`
	UpdateAvailable bool    `json:"updateAvailable"`
	CheckedAt       string  `json:"checkedAt"`
	Source          *string `json:"source"`
	ReleaseURL      string  `json:"releaseUrl"`
}

type VersionService struct {
	currentVersion string
	client         *http.Client
	now            func() time.Time
	ttl            time.Duration
	failureTTL     time.Duration
	apiURL         string

	mu       sync.Mutex
	cached   VersionResponse
	cachedAt time.Time
	hasCache bool
}

func NewVersionService(currentVersion string, client *http.Client) *VersionService {
	if strings.TrimSpace(currentVersion) == "" {
		currentVersion = CurrentVersion()
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &VersionService{
		currentVersion: currentVersion,
		client:         client,
		now:            func() time.Time { return time.Now().UTC() },
		ttl:            6 * time.Hour,
		failureTTL:     15 * time.Minute,
		apiURL:         latestReleaseAPIURL,
	}
}

func (s *VersionService) Status(ctx context.Context) VersionResponse {
	s.mu.Lock()
	if s.hasCache && s.now().Sub(s.cachedAt) < s.cacheTTLLocked() {
		cached := s.cached
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	response := s.fetch(ctx)
	s.mu.Lock()
	s.cached = response
	s.cachedAt = s.now()
	s.hasCache = true
	s.mu.Unlock()
	return response
}

func (s *VersionService) fetch(ctx context.Context) VersionResponse {
	checkedAt := s.now().UTC().Format(time.RFC3339)
	latest, err := s.fetchLatestReleaseVersion(ctx)
	if err != nil {
		return VersionResponse{
			CurrentVersion:  s.currentVersion,
			LatestVersion:   nil,
			UpdateAvailable: false,
			CheckedAt:       checkedAt,
			Source:          nil,
			ReleaseURL:      latestReleasePageURL,
		}
	}
	source := "github"
	return VersionResponse{
		CurrentVersion:  s.currentVersion,
		LatestVersion:   &latest,
		UpdateAvailable: isNewerVersion(latest, s.currentVersion),
		CheckedAt:       checkedAt,
		Source:          &source,
		ReleaseURL:      latestReleasePageURL,
	}
}

func (s *VersionService) fetchLatestReleaseVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "codex-lb/"+s.currentVersion)
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases API returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	version, ok := normalizeVersion(payload.TagName)
	if !ok {
		return "", fmt.Errorf("github release tag_name is not stable semver: %q", payload.TagName)
	}
	return version, nil
}

func (s *VersionService) cacheTTLLocked() time.Duration {
	if s.cached.Source == nil {
		return s.failureTTL
	}
	return s.ttl
}

var defaultVersionService = NewVersionService("", nil)

func Version(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(defaultVersionService.Status(r.Context()))
}

func CurrentVersion() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_LB_VERSION")); value != "" {
		return value
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	if version, ok := readPyProjectVersion("pyproject.toml"); ok {
		return version
	}
	return fallbackVersion
}

func readPyProjectVersion(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version = ") {
			value := strings.Trim(strings.TrimPrefix(line, "version = "), `"`)
			if value != "" {
				return value, true
			}
		}
	}
	return "", false
}

func normalizeVersion(value string) (string, bool) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return "", false
	}
	version := match[1] + "." + match[2] + "." + match[3]
	if match[4] != "" {
		version += "-" + match[4]
	}
	return version, true
}

type parsedVersion struct {
	major      int
	minor      int
	patch      int
	prerelease string
}

func parseVersion(value string) (parsedVersion, bool) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return parsedVersion{}, false
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	return parsedVersion{major: major, minor: minor, patch: patch, prerelease: match[4]}, true
}

func isNewerVersion(candidate, current string) bool {
	candidateVersion, ok := parseVersion(candidate)
	if !ok {
		return false
	}
	currentVersion, ok := parseVersion(current)
	if !ok {
		return false
	}
	if candidateVersion.major != currentVersion.major {
		return candidateVersion.major > currentVersion.major
	}
	if candidateVersion.minor != currentVersion.minor {
		return candidateVersion.minor > currentVersion.minor
	}
	if candidateVersion.patch != currentVersion.patch {
		return candidateVersion.patch > currentVersion.patch
	}
	return comparePrerelease(candidateVersion.prerelease, currentVersion.prerelease) > 0
}

func comparePrerelease(candidate, current string) int {
	if candidate == "" && current == "" {
		return 0
	}
	if candidate == "" {
		return 1
	}
	if current == "" {
		return -1
	}
	candidateParts := strings.Split(candidate, ".")
	currentParts := strings.Split(current, ".")
	for i := 0; i < len(candidateParts) && i < len(currentParts); i++ {
		if candidateParts[i] == currentParts[i] {
			continue
		}
		candidateNumber, candidateNumeric := parseNumericIdentifier(candidateParts[i])
		currentNumber, currentNumeric := parseNumericIdentifier(currentParts[i])
		switch {
		case candidateNumeric && currentNumeric:
			if candidateNumber > currentNumber {
				return 1
			}
			return -1
		case candidateNumeric:
			return -1
		case currentNumeric:
			return 1
		case candidateParts[i] > currentParts[i]:
			return 1
		default:
			return -1
		}
	}
	if len(candidateParts) == len(currentParts) {
		return 0
	}
	if len(candidateParts) > len(currentParts) {
		return 1
	}
	return -1
}

func parseNumericIdentifier(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}
