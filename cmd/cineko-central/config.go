package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	centralapi "github.com/cineko-org/central/internal/central/api"
	"github.com/cineko-org/central/internal/central/bootstrap"
	"github.com/cineko-org/central/internal/central/reconcile"
)

type applicationConfig struct {
	databaseURL            string
	enrollmentToken        string
	listenAddress          string
	minimumRuntimeVersion  string
	minimumBrowserRevision string
	trustedProxyCIDRs      string
	clientAuthorizer       central.ClientRegistrationAuthorizer
	probeBootstrapSigner   *bootstrap.Signer
	clientCredentials      []central.ClientCredentialSeed
	clientSessionTTL       time.Duration
	clientRefreshTTL       time.Duration
	clientReleases         []central.ClientRelease
	browserReleases        []central.BrowserRelease
	playwrightReleases     []central.PlaywrightRelease
	launcherReleases       []central.LauncherRelease
	probeReleases          []central.ProbeRelease
	releasePublishToken    string
	clientPINPepper        string
	adminCredentials       []centralapi.AdminCredential
	adminPasswordPepper    string
	adminSessionTTL        time.Duration
	reconciler             reconcile.Config
}

func loadConfig() (applicationConfig, error) {
	databaseURL, err := secretValue("CINEKO_CENTRAL_DATABASE_URL")
	if err != nil {
		return applicationConfig{}, err
	}
	enrollmentToken, err := secretValue("CINEKO_PROBE_ENROLLMENT_TOKEN")
	if err != nil {
		return applicationConfig{}, err
	}
	config := applicationConfig{
		databaseURL:            databaseURL,
		enrollmentToken:        enrollmentToken,
		listenAddress:          envString("CINEKO_CENTRAL_LISTEN", ":8080"),
		minimumRuntimeVersion:  strings.TrimSpace(os.Getenv("CINEKO_MINIMUM_PROBE_VERSION")),
		minimumBrowserRevision: strings.TrimSpace(os.Getenv("CINEKO_MINIMUM_BROWSER_REVISION")),
		trustedProxyCIDRs:      strings.TrimSpace(os.Getenv("CINEKO_TRUSTED_PROXY_CIDRS")),
	}
	if config.databaseURL == "" {
		return applicationConfig{}, errors.New("CINEKO_CENTRAL_DATABASE_URL is required")
	}
	if config.enrollmentToken == "" {
		return applicationConfig{}, errors.New("CINEKO_PROBE_ENROLLMENT_TOKEN is required")
	}
	for _, load := range []func(*applicationConfig) error{
		loadClientConfig, loadBootstrapConfig, loadReconcilerConfig,
	} {
		if err := load(&config); err != nil {
			return applicationConfig{}, err
		}
	}
	return config, nil
}

func loadClientConfig(config *applicationConfig) error {
	for _, load := range []func(*applicationConfig) error{
		loadClientAuthenticationConfig,
		loadReleaseBootstrapConfig,
		loadAdminConfig,
		loadClientSessionConfig,
	} {
		if err := load(config); err != nil {
			return err
		}
	}
	return nil
}

func loadClientAuthenticationConfig(config *applicationConfig) error {
	clientCredentials, err := secretValue("CINEKO_CLIENT_CREDENTIALS_JSON")
	if err != nil {
		return err
	}
	config.clientCredentials, err = parseClientCredentials(
		clientCredentials,
	)
	if err != nil {
		return err
	}
	config.clientPINPepper, err = secretValue("CINEKO_CLIENT_PIN_PEPPER")
	if err != nil {
		return err
	}
	if len(config.clientPINPepper) < 32 {
		return errors.New("CINEKO_CLIENT_PIN_PEPPER must be at least 32 characters")
	}
	return nil
}

func loadReleaseBootstrapConfig(config *applicationConfig) error {
	var err error
	config.clientReleases, err = parseClientReleases(
		strings.TrimSpace(os.Getenv("CINEKO_CLIENT_RELEASES_JSON")),
	)
	if err != nil {
		return err
	}
	config.browserReleases, err = parseBrowserReleases(
		strings.TrimSpace(os.Getenv("CINEKO_BROWSER_RELEASES_JSON")),
	)
	if err != nil {
		return err
	}
	config.playwrightReleases, err = parsePlaywrightReleases(
		strings.TrimSpace(os.Getenv("CINEKO_PLAYWRIGHT_RELEASES_JSON")),
	)
	if err != nil {
		return err
	}
	config.launcherReleases, err = parseLauncherReleases(
		strings.TrimSpace(os.Getenv("CINEKO_LAUNCHER_RELEASES_JSON")),
	)
	if err != nil {
		return err
	}
	config.probeReleases, err = parseProbeReleases(
		strings.TrimSpace(os.Getenv("CINEKO_PROBE_RELEASES_JSON")),
	)
	if err != nil {
		return err
	}
	config.releasePublishToken, err = secretValue("CINEKO_RELEASE_PUBLISH_TOKEN")
	if err != nil {
		return err
	}
	if config.releasePublishToken != "" && len(config.releasePublishToken) < 32 {
		return errors.New("CINEKO_RELEASE_PUBLISH_TOKEN must be at least 32 characters")
	}
	return nil
}

func loadAdminConfig(config *applicationConfig) error {
	adminCredentials, err := secretValue("CINEKO_ADMIN_CREDENTIALS_JSON")
	if err != nil {
		return err
	}
	config.adminCredentials, err = parseAdminCredentials(adminCredentials)
	if err != nil {
		return err
	}
	config.adminPasswordPepper, err = secretValue("CINEKO_ADMIN_PASSWORD_PEPPER")
	if err != nil {
		return err
	}
	if len(config.adminPasswordPepper) < 32 {
		return errors.New("CINEKO_ADMIN_PASSWORD_PEPPER must be at least 32 characters")
	}
	config.adminSessionTTL, err = envDuration("CINEKO_ADMIN_SESSION_TTL", 12*time.Hour)
	return err
}

func loadClientSessionConfig(config *applicationConfig) error {
	var err error
	config.clientSessionTTL, err = envDuration("CINEKO_CLIENT_SESSION_TTL", central.DefaultClientSessionTTL)
	if err != nil {
		return err
	}
	config.clientRefreshTTL, err = envDuration("CINEKO_CLIENT_REFRESH_TTL", central.DefaultClientRefreshTTL)
	return err
}

func secretValue(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	path := strings.TrimSpace(os.Getenv(name + "_FILE"))
	if value != "" && path != "" {
		return "", fmt.Errorf("%s and %s_FILE cannot both be set", name, name)
	}
	if path == "" {
		return value, nil
	}
	contents, err := os.ReadFile(path) // #nosec G304,G703 -- operator-configured secret-file path.
	if err != nil {
		return "", fmt.Errorf("read %s_FILE", name)
	}
	return strings.TrimSpace(string(contents)), nil
}

func loadBootstrapConfig(config *applicationConfig) error {
	var err error
	config.clientAuthorizer, err = loadClientAuthorizer(
		strings.TrimSpace(os.Getenv("CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS")),
		envString("CINEKO_PROBE_BOOTSTRAP_ISSUER", "cineko-central"),
		envString("CINEKO_PROBE_BOOTSTRAP_AUDIENCE", "cineko-probe"),
	)
	if err != nil {
		return err
	}
	config.probeBootstrapSigner, err = loadProbeBootstrapSigner(
		strings.TrimSpace(os.Getenv("CINEKO_PROBE_BOOTSTRAP_PRIVATE_KEY")),
		envString("CINEKO_PROBE_BOOTSTRAP_KEY_ID", "primary"),
		envString("CINEKO_PROBE_BOOTSTRAP_ISSUER", "cineko-central"),
		envString("CINEKO_PROBE_BOOTSTRAP_AUDIENCE", "cineko-probe"),
	)
	return err
}

func loadReconcilerConfig(config *applicationConfig) error {
	var err error
	config.reconciler.TickInterval, err = envDuration("CINEKO_RECONCILE_INTERVAL", 5*time.Second)
	if err != nil {
		return err
	}
	config.reconciler.ProbeHeartbeatTTL, err = envDuration("CINEKO_PROBE_HEARTBEAT_TTL", 90*time.Second)
	if err != nil {
		return err
	}
	config.reconciler.OfflineRetention, err = envDuration("CINEKO_PROBE_OFFLINE_RETENTION", 30*24*time.Hour)
	if err != nil {
		return err
	}
	config.reconciler.RetryMinimum, err = envDuration("CINEKO_ASSIGNMENT_RETRY_MIN", time.Second)
	if err != nil {
		return err
	}
	config.reconciler.RetryMaximum, err = envDuration("CINEKO_ASSIGNMENT_RETRY_MAX", 5*time.Second)
	if err != nil {
		return err
	}
	config.reconciler.BatchSize, err = envInt("CINEKO_RECONCILE_BATCH_SIZE", 100)
	return err
}

func loadProbeBootstrapSigner(
	keyPath string,
	keyID string,
	issuer string,
	audience string,
) (*bootstrap.Signer, error) {
	if keyPath == "" {
		return nil, nil
	}
	if !filepath.IsAbs(keyPath) {
		return nil, errors.New("CINEKO_PROBE_BOOTSTRAP_PRIVATE_KEY must be an absolute path")
	}
	contents, err := os.ReadFile(keyPath) // #nosec G304,G703 -- operator-configured absolute private-key path.
	if err != nil {
		return nil, fmt.Errorf("read Probe bootstrap private key: %w", err)
	}
	key, err := bootstrap.ParsePrivateKeyPEM(contents)
	if err != nil {
		return nil, err
	}
	signer, err := bootstrap.NewSigner(issuer, audience, keyID, key)
	if err != nil {
		return nil, err
	}
	return signer, nil
}

func parseClientReleases(value string) ([]central.ClientRelease, error) {
	return parseReleaseJSON[central.ClientRelease]("CINEKO_CLIENT_RELEASES_JSON", value)
}

func parseBrowserReleases(value string) ([]central.BrowserRelease, error) {
	return parseReleaseJSON[central.BrowserRelease]("CINEKO_BROWSER_RELEASES_JSON", value)
}

func parsePlaywrightReleases(value string) ([]central.PlaywrightRelease, error) {
	return parseReleaseJSON[central.PlaywrightRelease]("CINEKO_PLAYWRIGHT_RELEASES_JSON", value)
}

func parseLauncherReleases(value string) ([]central.LauncherRelease, error) {
	return parseReleaseJSON[central.LauncherRelease]("CINEKO_LAUNCHER_RELEASES_JSON", value)
}

func parseProbeReleases(value string) ([]central.ProbeRelease, error) {
	return parseReleaseJSON[central.ProbeRelease]("CINEKO_PROBE_RELEASES_JSON", value)
}

func parseReleaseJSON[T any](name string, value string) ([]T, error) {
	if value == "" {
		return nil, nil
	}
	var releases []T
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s must contain one JSON value", name)
	}
	return releases, nil
}

func parseClientCredentials(value string) ([]central.ClientCredentialSeed, error) {
	if value == "" {
		return nil, nil
	}
	var input []struct {
		UserID      string `json:"userId"`
		DisplayName string `json:"displayName"`
		AccessToken string `json:"accessToken"`
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode CINEKO_CLIENT_CREDENTIALS_JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("CINEKO_CLIENT_CREDENTIALS_JSON must contain one JSON value")
	}
	credentials := make([]central.ClientCredentialSeed, len(input))
	for index, item := range input {
		credentials[index] = central.ClientCredentialSeed{
			UserID: item.UserID, DisplayName: item.DisplayName, AccessToken: item.AccessToken,
		}
	}
	return credentials, nil
}

func parseAdminCredentials(value string) ([]centralapi.AdminCredential, error) {
	if value == "" {
		return nil, nil
	}
	var credentials []centralapi.AdminCredential
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil {
		return nil, fmt.Errorf("decode CINEKO_ADMIN_CREDENTIALS_JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("CINEKO_ADMIN_CREDENTIALS_JSON must contain one JSON value")
	}
	return credentials, nil
}

func loadClientAuthorizer(
	keySpec string,
	issuer string,
	audience string,
) (central.ClientRegistrationAuthorizer, error) {
	if keySpec == "" {
		return nil, nil
	}
	keys, err := bootstrap.LoadPublicKeyFiles(keySpec)
	if err != nil {
		return nil, fmt.Errorf("load Probe bootstrap public keys: %w", err)
	}
	return bootstrap.NewVerifier(issuer, audience, keys, 15*time.Second)
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}

func envInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}
