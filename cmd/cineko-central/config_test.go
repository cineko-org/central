package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cineko-org/central/internal/central"
)

func TestSecretValueSupportsFilesWithoutLeakingContents(t *testing.T) {
	const name = "CINEKO_TEST_SECRET"
	secret := "secret-value-that-must-not-appear"
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("  "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(name, "")
	t.Setenv(name+"_FILE", path)
	if value, err := secretValue(name); err != nil || value != secret {
		t.Fatalf("secretValue(file) = %q, %v", value, err)
	}
	t.Setenv(name, secret)
	if _, err := secretValue(name); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("conflicting secret sources error = %v", err)
	}
	t.Setenv(name, "")
	t.Setenv(name+"_FILE", filepath.Join(t.TempDir(), secret))
	if _, err := secretValue(name); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("secret file error leaks content = %v", err)
	}
}

func TestLoadConfigRequiresProductionClientCredentials(t *testing.T) {
	t.Setenv("CINEKO_CENTRAL_DATABASE_URL", "postgres://cineko:test@localhost/cineko")
	t.Setenv("CINEKO_PROBE_ENROLLMENT_TOKEN", "probe-enrollment-token")
	t.Setenv("CINEKO_CLIENT_CREDENTIALS_JSON", `[{"userId":"user","displayName":"User","accessToken":"0123456789abcdef0123456789abcdef"}]`)
	t.Setenv("CINEKO_CLIENT_PIN_PEPPER", "0123456789abcdef0123456789abcdef")
	t.Setenv("CINEKO_ADMIN_CREDENTIALS_JSON", `[{"userId":"admin","displayName":"Admin","password":"admin-password"}]`)
	t.Setenv("CINEKO_ADMIN_PASSWORD_PEPPER", "abcdef0123456789abcdef0123456789")
	t.Setenv("CINEKO_CLIENT_RELEASES_JSON", `{"releases":[{"channel":"stable","platform":"darwin","architecture":"arm64","version":"1.0.0","minimumLauncherVersion":"1.0.0","minimumBrowserRevision":"1234","playwrightVersion":"1.61.1","artifact":{"url":"https://cdn.example/client.zip","size":"1","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","executable":"Cineko.app/Contents/MacOS/Cineko"},"probeBootstrapPublicKeys":{"primary":"-----BEGIN PUBLIC KEY-----\nplaceholder\n-----END PUBLIC KEY-----\n"},"publishedAt":"2026-08-10T00:00:00Z"}]}`)
	t.Setenv("CINEKO_BROWSER_RELEASES_JSON", `{"releases":[{"channel":"stable","platform":"darwin","architecture":"arm64","revision":"1234","compatiblePlaywrightVersions":["1.61.1"],"artifact":{"url":"https://cdn.example/browser.zip","size":"1","sha256":"1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","executable":"chromium/Chromium"},"publishedAt":"2026-08-10T00:00:00Z"}]}`)
	t.Setenv("CINEKO_PLAYWRIGHT_RELEASES_JSON", `{"releases":[{"channel":"stable","platform":"darwin","architecture":"arm64","version":"1.61.1","artifact":{"url":"https://cdn.example/driver.zip","size":"1","sha256":"2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","executable":"driver/playwright"},"publishedAt":"2026-08-10T00:00:00Z"}]}`)
	t.Setenv("CINEKO_LAUNCHER_RELEASES_JSON", `{"releases":[{"channel":"stable","platform":"darwin","architecture":"arm64","version":"1.0.0","launcher":{"url":"https://cdn.example/launcher.zip","size":"1","sha256":"3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","executable":"Cineko Launcher.app/Contents/MacOS/Cineko Launcher"},"publishedAt":"2026-08-10T00:00:00Z"}]}`)
	t.Setenv("CINEKO_PROBE_RELEASES_JSON", `{"releases":[{"channel":"stable","version":"1.0.0","browserRevision":"1234","image":"registry.example.com/example/cineko-probe","imageDigest":"sha256:4123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","publishedAt":"2026-08-10T00:00:00Z"}]}`)
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.clientCredentials) != 1 || config.clientCredentials[0].UserID != "user" {
		t.Fatalf("client credentials = %+v", config.clientCredentials)
	}
	if config.clientRefreshTTL != central.DefaultClientRefreshTTL {
		t.Fatalf("client refresh TTL = %v", config.clientRefreshTTL)
	}
	if len(config.clientReleases.GetReleases()) != 1 || config.clientReleases.GetReleases()[0].GetVersion() != "1.0.0" {
		t.Fatalf("client releases = %+v", config.clientReleases)
	}
	if len(config.browserReleases.GetReleases()) != 1 || config.browserReleases.GetReleases()[0].GetRevision() != "1234" {
		t.Fatalf("browser releases = %+v", config.browserReleases)
	}
	if len(config.playwrightReleases.GetReleases()) != 1 || config.playwrightReleases.GetReleases()[0].GetVersion() != "1.61.1" {
		t.Fatalf("Playwright releases = %+v", config.playwrightReleases)
	}
	if len(config.launcherReleases.GetReleases()) != 1 || config.launcherReleases.GetReleases()[0].GetVersion() != "1.0.0" {
		t.Fatalf("launcher releases = %+v", config.launcherReleases)
	}
	if len(config.probeReleases.GetReleases()) != 1 || config.probeReleases.GetReleases()[0].GetVersion() != "1.0.0" {
		t.Fatalf("Probe releases = %+v", config.probeReleases)
	}
	if len(config.adminCredentials) != 1 || config.adminCredentials[0].UserID != "admin" {
		t.Fatalf("admin credentials = %+v", config.adminCredentials)
	}
	t.Setenv("CINEKO_CLIENT_PIN_PEPPER", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing Client PIN pepper accepted")
	}
	t.Setenv("CINEKO_CLIENT_PIN_PEPPER", "0123456789abcdef0123456789abcdef")
	t.Setenv("CINEKO_ADMIN_CREDENTIALS_JSON", "")
	if config, err := loadConfig(); err != nil || len(config.adminCredentials) != 0 {
		t.Fatalf("optional bootstrap admin credentials = %+v, %v", config.adminCredentials, err)
	}
	t.Setenv("CINEKO_ADMIN_CREDENTIALS_JSON", `[{"userId":"admin","displayName":"Admin","password":"admin-password"}]`)
	t.Setenv("CINEKO_ADMIN_PASSWORD_PEPPER", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing admin password pepper accepted")
	}
	t.Setenv("CINEKO_ADMIN_PASSWORD_PEPPER", "abcdef0123456789abcdef0123456789")

	t.Setenv("CINEKO_CLIENT_CREDENTIALS_JSON", "")
	if config, err := loadConfig(); err != nil || len(config.clientCredentials) != 0 {
		t.Fatalf("optional client credentials = %+v, %v", config.clientCredentials, err)
	}
	t.Setenv("CINEKO_CLIENT_CREDENTIALS_JSON", `[] {}`)
	if _, err := loadConfig(); err == nil {
		t.Fatal("multiple client credential values accepted")
	}
	t.Setenv("CINEKO_CLIENT_CREDENTIALS_JSON", `[{"userId":"user","displayName":"User","accessToken":"0123456789abcdef0123456789abcdef"}]`)
	t.Setenv("CINEKO_CLIENT_RELEASES_JSON", "")
	t.Setenv("CINEKO_LAUNCHER_RELEASES_JSON", "")
	t.Setenv("CINEKO_PROBE_RELEASES_JSON", "")
	if config, err := loadConfig(); err != nil || len(config.clientReleases.GetReleases()) != 0 ||
		len(config.launcherReleases.GetReleases()) != 0 || len(config.probeReleases.GetReleases()) != 0 {
		t.Fatalf("optional release bootstrap = %+v, %v", config, err)
	}
	t.Setenv("CINEKO_RELEASE_PUBLISH_TOKEN", "short")
	if _, err := loadConfig(); err == nil {
		t.Fatal("short release publish token accepted")
	}
}
