package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scriptDir builds a minimal plugin-root working directory containing just
// what the fetch-or-build script needs: itself and the manifest.
func scriptDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for src, dst := range map[string]string{
		"scripts/fetch-or-build.sh": filepath.Join(dir, "scripts", "fetch-or-build.sh"),
		"herdr-plugin.toml":         filepath.Join(dir, "herdr-plugin.toml"),
	} {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runScript(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"scripts/fetch-or-build.sh"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func manifestVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("herdr-plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, `version = "`); ok {
			return strings.TrimSuffix(v, `"`)
		}
	}
	t.Fatal("no version in herdr-plugin.toml")
	return ""
}

func TestFetchScriptPrintAsset(t *testing.T) {
	dir := scriptDir(t)
	v := manifestVersion(t)
	cases := []struct{ unameS, unameM, want string }{
		{"Darwin", "arm64", "herdr-window-title_" + v + "_darwin_arm64"},
		{"Darwin", "x86_64", "herdr-window-title_" + v + "_darwin_amd64"},
		{"Linux", "aarch64", "herdr-window-title_" + v + "_linux_arm64"},
		{"Linux", "x86_64", "herdr-window-title_" + v + "_linux_amd64"},
	}
	for _, tc := range cases {
		out, err := runScript(t, dir,
			[]string{"HWT_UNAME_S=" + tc.unameS, "HWT_UNAME_M=" + tc.unameM}, "--print-asset")
		if err != nil {
			t.Fatalf("%s/%s: %v\n%s", tc.unameS, tc.unameM, err, out)
		}
		if strings.TrimSpace(out) != tc.want {
			t.Errorf("%s/%s: asset = %q, want %q", tc.unameS, tc.unameM, strings.TrimSpace(out), tc.want)
		}
	}

	if out, err := runScript(t, dir,
		[]string{"HWT_UNAME_S=SunOS", "HWT_UNAME_M=sparc"}, "--print-asset"); err == nil {
		t.Errorf("unsupported platform succeeded: %s", out)
	}
}

// fakeRelease lays out a release directory servable over file:// with one
// asset and a goreleaser-style checksums.txt.
func fakeRelease(t *testing.T, version, asset string, payload []byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "v"+version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, asset), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%x  %s\n", sha256.Sum256(payload), asset)
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sum), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(dir)
}

func TestFetchScriptDownloadsPrebuilt(t *testing.T) {
	dir := scriptDir(t)
	v := manifestVersion(t)
	payload := []byte("#!/bin/sh\necho fake-prebuilt\n")
	base := fakeRelease(t, v, "herdr-window-title_"+v+"_darwin_arm64", payload)

	out, err := runScript(t, dir, []string{
		"HWT_UNAME_S=Darwin", "HWT_UNAME_M=arm64", "HWT_BASE_URL=file://" + base,
	})
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "bin", "herdr-window-title"))
	if err != nil {
		t.Fatalf("installed binary: %v", err)
	}
	if string(got) != string(payload) {
		t.Error("installed binary does not match release asset")
	}
	info, err := os.Stat(filepath.Join(dir, "bin", "herdr-window-title"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("installed binary is not executable")
	}
}

func TestFetchScriptChecksumMismatchFailsHard(t *testing.T) {
	dir := scriptDir(t)
	v := manifestVersion(t)
	asset := "herdr-window-title_" + v + "_darwin_arm64"
	base := fakeRelease(t, v, asset, []byte("real payload"))
	// Corrupt the asset after the checksum was computed.
	if err := os.WriteFile(filepath.Join(base, "v"+v, asset), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runScript(t, dir, []string{
		"HWT_UNAME_S=Darwin", "HWT_UNAME_M=arm64", "HWT_BASE_URL=file://" + base,
	})
	if err == nil {
		t.Fatalf("checksum mismatch did not fail; output: %s", out)
	}
	if !strings.Contains(out, "checksum") {
		t.Errorf("failure output %q does not mention checksum", out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "bin", "herdr-window-title")); statErr == nil {
		t.Error("binary was installed despite checksum mismatch")
	}
}

func TestFetchScriptFallsBackToGoBuild(t *testing.T) {
	// Run in the real plugin root: no release is reachable, so the script must
	// build from source, which needs the module. Rebuilding the checked-in
	// bin/ binary in place is harmless.
	out, err := runScript(t, ".", []string{"HWT_BASE_URL=file:///nonexistent"})
	if err != nil {
		t.Fatalf("fallback build: %v\n%s", err, out)
	}
	if !strings.Contains(out, "building from source") {
		t.Errorf("output %q does not mention source build", out)
	}
	if _, err := os.Stat("bin/herdr-window-title"); err != nil {
		t.Fatalf("binary missing after fallback build: %v", err)
	}
}
