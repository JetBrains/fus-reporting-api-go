package fus

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type RegionCode string

const (
	RegionAll RegionCode = "ALL"
	RegionCN  RegionCode = "CN"
)

const (
	defaultSendEndpoint  = "https://analytics.services.jetbrains.com/fus/v5/send/"
	configURLTemplateAll = "https://resources.jetbrains.com/storage/fus/config/v4/%s/%s.json"
	configURLTemplateCN  = "https://resources.jetbrains.com.cn/storage/fus/config/v4/%s/%s.json"
	configCacheFile      = "fus_config.json"
	configCacheTTL       = 10 * time.Minute
	configFetchTimeout   = 5 * time.Second
)

type FUSConfig struct {
	SendEndpoint     string `json:"send_endpoint"`
	MetadataEndpoint string `json:"metadata_endpoint,omitempty"`
	Salt             string `json:"salt"`
	SaltRevision     int    `json:"salt_revision"`
	FetchedAt        int64  `json:"fetched_at"`
}

// SchemeURL returns the full metadata URL for the given product code,
// mirroring JVM ConfigurationVersion.provideMetadataProductUrl.
func (c *FUSConfig) SchemeURL(productCode string) string {
	base := c.MetadataEndpoint
	if base == "" {
		return ""
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + productCode + ".json"
}

// LoadOrFetchConfig returns the public FUS config (send endpoint, options); salt may be empty — supply it via RecorderConfig.AnonymizationSalt.
func LoadOrFetchConfig(recorderID, productCode, productVersion, dataDir string, region RegionCode) (*FUSConfig, error) {
	cachePath := filepath.Join(dataDir, configCacheFile)
	cached, cacheErr := loadCachedConfig(cachePath)
	cacheUsable := cacheErr == nil && cached.SendEndpoint != ""

	if cacheUsable && time.Since(time.Unix(cached.FetchedAt, 0)) < configCacheTTL {
		return cached, nil
	}

	cfg, err := fetchConfig(recorderID, productCode, productVersion, region)
	if err == nil {
		cfg.FetchedAt = time.Now().Unix()
		writeCache(cachePath, cfg)
		return cfg, nil
	}

	if cacheUsable {
		return cached, nil
	}

	return nil, fmt.Errorf("fus: no usable config (no cached endpoint and fetch failed: %w)", err)
}

func writeCache(path string, cfg *FUSConfig) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}

func loadCachedConfig(path string) (*FUSConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg FUSConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type remoteConfigResponse struct {
	ProductCode string                `json:"productCode"`
	Versions    []remoteConfigVersion `json:"versions"`
}

type remoteConfigVersion struct {
	MajorBuildVersionBorders *versionBorders       `json:"majorBuildVersionBorders"`
	Endpoints                remoteConfigEndpoints `json:"endpoints"`
	Options                  remoteConfigOptions   `json:"options"`
}

type versionBorders struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type remoteConfigEndpoints struct {
	Send     string `json:"send"`
	Metadata string `json:"metadata"`
}

type remoteConfigOptions struct {
	IDSalt         string `json:"id_salt"`
	IDSaltRevision string `json:"id_salt_revision"`
}

func FetchTestConfig(recorderID, productCode string) (*FUSConfig, error) {
	return fetchConfig("test/"+recorderID, productCode, "", RegionAll)
}

func configURLTemplate(region RegionCode) string {
	if region == RegionCN {
		return configURLTemplateCN
	}
	return configURLTemplateAll
}

func fetchConfig(recorderID, productCode, productVersion string, region RegionCode) (*FUSConfig, error) {
	url := fmt.Sprintf(configURLTemplate(region), recorderID, productCode)

	client := &http.Client{Timeout: configFetchTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch fus config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fus config: status %d", resp.StatusCode)
	}

	var remote remoteConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		return nil, fmt.Errorf("decode fus config: %w", err)
	}

	v := findMatchingVersion(remote.Versions, productVersion)
	if v == nil {
		return nil, fmt.Errorf("fus config: response contained no versions")
	}

	cfg := &FUSConfig{SendEndpoint: defaultSendEndpoint}
	if v.Endpoints.Send != "" {
		cfg.SendEndpoint = v.Endpoints.Send
	}
	cfg.MetadataEndpoint = v.Endpoints.Metadata
	cfg.Salt = v.Options.IDSalt
	if v.Options.IDSaltRevision != "" {
		cfg.SaltRevision, _ = strconv.Atoi(v.Options.IDSaltRevision)
	}
	return cfg, nil
}

// findMatchingVersion selects the first version whose majorBuildVersionBorders
// accepts the product version. Falls back to the first version if none match.
func findMatchingVersion(versions []remoteConfigVersion, productVersion string) *remoteConfigVersion {
	if len(versions) == 0 {
		return nil
	}

	build := parseMajorVersion(productVersion)
	if !isValidMajorVersion(build) {
		return &versions[0]
	}

	for i := range versions {
		v := &versions[i]
		if v.MajorBuildVersionBorders == nil {
			continue
		}
		if acceptVersion(v.MajorBuildVersionBorders, productVersion) {
			return v
		}
	}

	return &versions[0]
}

// acceptVersion checks if the product version falls within [from, to).
func acceptVersion(borders *versionBorders, current string) bool {
	build := parseMajorVersion(current)
	if !isValidMajorVersion(build) {
		return false
	}

	from := parseMajorVersion(borders.From)
	to := parseMajorVersion(borders.To)

	if !isValidMajorVersion(from) && !isValidMajorVersion(to) {
		return false
	}

	if isValidMajorVersion(from) && compareMajorVersions(from, build) > 0 {
		return false
	}
	if isValidMajorVersion(to) && compareMajorVersions(to, build) <= 0 {
		return false
	}

	return true
}

func parseMajorVersion(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ".")
	result := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		result[i] = n
	}
	if len(result) == 1 {
		result = append(result, 0)
	}
	return result
}

func isValidMajorVersion(v []int) bool {
	return len(v) > 0 && v[0] > 0
}

func compareMajorVersions(a, b []int) int {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := range maxLen {
		va, vb := 0, 0
		if i < len(a) {
			va = a[i]
		}
		if i < len(b) {
			vb = b[i]
		}
		if va != vb {
			return va - vb
		}
	}
	return 0
}
