// Package lifecycle owns module install: fetch, verify, stage alongside,
// health-gated promote, N-1 retention, rollback. The rules are spec §8's:
// verify before exec, stage then promote, swap atomically, fail closed.
//
// On disk each installed module is versioned:
//
//	modules/<id>/current            version string
//	modules/<id>/previous           version string (N-1 retention)
//	modules/<id>/versions/<ver>/    binary, module.manifest.json, config.json
package lifecycle

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/layout"
	"github.com/deploymenttheory/weaveplatform-agent/internal/manifestverify"
	"github.com/deploymenttheory/weaveplatform-agent/internal/supervise"
	"github.com/deploymenttheory/weaveplatform-api/manifest"
)

// Manager performs installs and rollbacks against the supervisor.
type Manager struct {
	Log        *slog.Logger
	Layout     layout.Layout
	Verifier   supervise.Verifier
	Supervisor *supervise.Supervisor
	// RootPub verifies channel manifests; nil disables channel installs
	// (local installs still work in dev builds).
	RootPub ed25519.PublicKey
	// ManifestURL is where the signed channel manifest bundle lives:
	// <base>/manifest.json{,.sig} and <base>/signing.pub{,.sig}.
	ManifestURL string
	// Client for fetches; nil gets a 5-minute-timeout default.
	Client *http.Client
	// GateTimeout bounds the post-promote health gate; zero gets 30s.
	GateTimeout time.Duration
	// GateStable is how long the module must stay running to pass the
	// gate; zero gets 3s.
	GateStable time.Duration
}

func (m *Manager) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// InstallLocal installs a module from a local directory containing the
// binary and its module.manifest.json (dev builds' path — the verifier
// still runs).
func (m *Manager) InstallLocal(ctx context.Context, dir string) (string, error) {
	mf, err := manifest.Load(filepath.Join(dir, "module.manifest.json"))
	if err != nil {
		return "", err
	}
	bin, err := findBinary(dir, mf.ID)
	if err != nil {
		return "", err
	}
	stage, err := m.stageFiles(mf, bin, filepath.Join(dir, "config.json"))
	if err != nil {
		return "", err
	}
	if err := m.promote(ctx, mf, stage); err != nil {
		return "", err
	}
	return mf.Version, nil
}

// Install fetches and installs a module per the signed channel manifest.
// version empty means the channel's current.
func (m *Manager) Install(ctx context.Context, moduleID, version string) (string, error) {
	if m.RootPub == nil || m.ManifestURL == "" {
		return "", fmt.Errorf("lifecycle: channel installs unavailable: no manifest source configured")
	}
	ch, err := m.fetchChannel(ctx)
	if err != nil {
		return "", err
	}
	cm, ok := ch.Module(moduleID)
	if !ok {
		return "", fmt.Errorf("lifecycle: module %q not in channel %q", moduleID, ch.Channel)
	}
	if version != "" && cm.Version != version {
		return "", fmt.Errorf("lifecycle: channel %q carries %s@%s, not %s", ch.Channel, moduleID, cm.Version, version)
	}
	art, ok := manifest.ArtifactForHost(cm.Artifacts)
	if !ok {
		return "", fmt.Errorf("lifecycle: no artifact for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// Fetch to a temp file and check the digest before anything else
	// touches it.
	tmp, err := m.download(ctx, art.URL, art.Digest, art.Size)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp) //nolint:errcheck

	mf := cm.ModuleManifest([]manifest.Platform{{OS: runtime.GOOS, Arch: runtime.GOARCH}})
	stage, err := m.stageFiles(mf, tmp, "")
	if err != nil {
		return "", err
	}
	if err := m.promote(ctx, mf, stage); err != nil {
		return "", err
	}
	return mf.Version, nil
}

// Rollback flips a module to its retained previous version.
func (m *Manager) Rollback(ctx context.Context, moduleID string) (string, error) {
	moduleDir := filepath.Join(m.Layout.ModulesDir, moduleID)
	prev, err := os.ReadFile(filepath.Join(moduleDir, "previous"))
	if err != nil {
		return "", fmt.Errorf("lifecycle: no previous version retained for %s", moduleID)
	}
	prevVersion := strings.TrimSpace(string(prev))
	verDir := filepath.Join(moduleDir, "versions", prevVersion)
	mf, err := manifest.Load(filepath.Join(verDir, "module.manifest.json"))
	if err != nil {
		return "", err
	}
	current, _ := os.ReadFile(filepath.Join(moduleDir, "current"))

	if err := m.activate(ctx, mf, verDir); err != nil {
		return "", err
	}
	// The rolled-back-from version becomes "previous" so an operator can
	// flip forward again.
	writeFileString(filepath.Join(moduleDir, "previous"), strings.TrimSpace(string(current))) //nolint:errcheck
	return prevVersion, nil
}

// stageFiles copies the binary (and optional config) into the staging
// area and verifies the signature there — before anything reaches the
// install tree.
func (m *Manager) stageFiles(mf *manifest.Manifest, binPath, configPath string) (string, error) {
	stage := filepath.Join(m.Layout.StagingDir, mf.ID, mf.Version)
	if err := os.RemoveAll(stage); err != nil {
		return "", err
	}
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return "", err
	}
	stagedBin := filepath.Join(stage, binaryName(mf.ID))
	if err := copyFile(binPath, stagedBin, 0o700); err != nil {
		return "", err
	}
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			if err := copyFile(configPath, filepath.Join(stage, "config.json"), 0o600); err != nil {
				return "", err
			}
		}
	}
	mfBytes, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, "module.manifest.json"), mfBytes, 0o600); err != nil {
		return "", err
	}
	// Verify before anything can exec it. Non-negotiable.
	if err := m.Verifier.Verify(stagedBin, mf); err != nil {
		os.RemoveAll(stage) //nolint:errcheck
		return "", fmt.Errorf("lifecycle: staged binary failed verification: %w", err)
	}
	return stage, nil
}

// promote moves the staged version into the install tree, flips current,
// swaps the running process, and health-gates the result; on failure it
// restores the old version and reports the error.
func (m *Manager) promote(ctx context.Context, mf *manifest.Manifest, stage string) error {
	moduleDir := filepath.Join(m.Layout.ModulesDir, mf.ID)
	verDir := filepath.Join(moduleDir, "versions", mf.Version)
	if err := os.MkdirAll(filepath.Dir(verDir), 0o700); err != nil {
		return err
	}
	if err := os.RemoveAll(verDir); err != nil {
		return err
	}
	if err := os.Rename(stage, verDir); err != nil {
		return fmt.Errorf("lifecycle: promoting stage: %w", err)
	}

	oldVersion := ""
	if b, err := os.ReadFile(filepath.Join(moduleDir, "current")); err == nil {
		oldVersion = strings.TrimSpace(string(b))
	}

	if err := m.activate(ctx, mf, verDir); err != nil {
		// Roll back to the old version if there was one.
		if oldVersion != "" && oldVersion != mf.Version {
			oldDir := filepath.Join(moduleDir, "versions", oldVersion)
			if oldMf, lerr := manifest.Load(filepath.Join(oldDir, "module.manifest.json")); lerr == nil {
				if rerr := m.activate(ctx, oldMf, oldDir); rerr != nil {
					m.Log.Error("rollback after failed promote also failed", "module", mf.ID, "err", rerr)
				} else {
					m.Log.Warn("promote failed; rolled back", "module", mf.ID, "to", oldVersion)
				}
			}
		}
		return fmt.Errorf("lifecycle: promote of %s@%s failed health gate: %w", mf.ID, mf.Version, err)
	}

	// Success: retain exactly N-1.
	if oldVersion != "" && oldVersion != mf.Version {
		writeFileString(filepath.Join(moduleDir, "previous"), oldVersion) //nolint:errcheck
		m.prune(moduleDir, mf.Version, oldVersion)
	}
	m.Log.Info("module promoted", "module", mf.ID, "version", mf.Version)
	return nil
}

// activate points current at verDir's version, swaps the runner, and
// health-gates.
func (m *Manager) activate(ctx context.Context, mf *manifest.Manifest, verDir string) error {
	moduleDir := filepath.Join(m.Layout.ModulesDir, mf.ID)
	if err := writeFileString(filepath.Join(moduleDir, "current"), mf.Version); err != nil {
		return err
	}
	bin, err := findBinary(verDir, mf.ID)
	if err != nil {
		return err
	}
	var config []byte
	if b, err := os.ReadFile(filepath.Join(verDir, "config.json")); err == nil {
		config = b
	}
	m.Supervisor.Replace(ctx, supervise.Spec{Manifest: mf, BinPath: bin, Config: config})
	return m.healthGate(ctx, mf.ID)
}

// healthGate waits for the module to be running and stay running.
func (m *Manager) healthGate(ctx context.Context, id string) error {
	timeout := m.GateTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	stable := m.GateStable
	if stable == 0 {
		stable = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var runningSince time.Time
	for time.Now().Before(deadline) {
		st, ok := m.statusOf(id)
		if !ok {
			return fmt.Errorf("module vanished from supervisor")
		}
		switch st.State {
		case supervise.StateRunning:
			if runningSince.IsZero() {
				runningSince = time.Now()
			} else if time.Since(runningSince) >= stable {
				return nil
			}
		case supervise.StateBreaker, supervise.StateUnsupportedProtocol, supervise.StateRequirementsUnmet:
			return fmt.Errorf("module state %s: %s", st.State, st.Detail)
		default:
			runningSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("health gate timed out")
}

func (m *Manager) statusOf(id string) (supervise.Status, bool) {
	for _, st := range m.Supervisor.Statuses() {
		if st.ID == id {
			return st, true
		}
	}
	return supervise.Status{}, false
}

// prune deletes every version except keep and prev.
func (m *Manager) prune(moduleDir, keep, prev string) {
	entries, err := os.ReadDir(filepath.Join(moduleDir, "versions"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != keep && e.Name() != prev {
			os.RemoveAll(filepath.Join(moduleDir, "versions", e.Name())) //nolint:errcheck
		}
	}
}

// fetchChannel downloads and verifies the signed channel manifest bundle.
func (m *Manager) fetchChannel(ctx context.Context) (*manifest.ChannelManifest, error) {
	get := func(name string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			strings.TrimSuffix(m.ManifestURL, "/")+"/"+name, nil)
		if err != nil {
			return nil, err
		}
		resp, err := m.client().Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: %s", name, resp.Status)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	}
	var b manifestverify.Bundle
	var err error
	if b.Manifest, err = get("manifest.json"); err != nil {
		return nil, err
	}
	if b.ManifestSig, err = get("manifest.json.sig"); err != nil {
		return nil, err
	}
	if b.SigningKey, err = get("signing.pub"); err != nil {
		return nil, err
	}
	if b.SigningKeySig, err = get("signing.pub.sig"); err != nil {
		return nil, err
	}
	return manifestverify.Verify(m.RootPub, b)
}

// download fetches url to a temp file, enforcing digest and size.
func (m *Manager) download(ctx context.Context, url, digest string, size int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := m.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("lifecycle: artifact fetch: %s", resp.Status)
	}
	tmp, err := os.CreateTemp(m.Layout.StagingDir, "dl-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, size+1))
	tmp.Close() //nolint:errcheck
	if err != nil {
		os.Remove(tmp.Name()) //nolint:errcheck
		return "", err
	}
	if n != size {
		os.Remove(tmp.Name()) //nolint:errcheck
		return "", fmt.Errorf("lifecycle: artifact size %d, manifest says %d", n, size)
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != digest {
		os.Remove(tmp.Name()) //nolint:errcheck
		return "", fmt.Errorf("lifecycle: artifact digest %s, manifest says %s", got, digest)
	}
	return tmp.Name(), nil
}

func binaryName(id string) string {
	if runtime.GOOS == "windows" {
		return id + ".exe"
	}
	return id
}

func findBinary(dir, id string) (string, error) {
	for _, cand := range []string{binaryName(id), "module", "module.exe"} {
		p := filepath.Join(dir, cand)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("lifecycle: no binary for %s in %s", id, dir)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close() //nolint:errcheck
		return err
	}
	return out.Close()
}

func writeFileString(path, s string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(s+"\n"), 0o600)
}
