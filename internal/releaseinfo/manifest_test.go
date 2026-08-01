package releaseinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateCreatesVerifiableReleaseMetadata(t *testing.T) {
	directory := t.TempDir()
	options := validOptions()
	writeExpectedAssets(t, directory, options.Tag, "")

	generated, err := Generate(directory, options)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if generated.Manifest.SchemaVersion != ManifestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", generated.Manifest.SchemaVersion, ManifestSchemaVersion)
	}
	if generated.Manifest.Version != "1.2.3" || generated.Manifest.ReleaseSequence != 42 {
		t.Fatalf("unexpected version metadata: %+v", generated.Manifest)
	}
	if generated.Manifest.SigningIdentity != "https://github.com/Heee-oh/FlowRoutine/.github/workflows/release.yml@refs/tags/v1.2.3" {
		t.Fatalf("signing identity = %q", generated.Manifest.SigningIdentity)
	}
	if len(generated.Manifest.Artifacts) != 3 {
		t.Fatalf("artifact count = %d, want 3", len(generated.Manifest.Artifacts))
	}
	if generated.Manifest.Artifacts[1].NativeVerification != "apple-codesign-notarized+sigstore" {
		t.Fatalf("macOS verification = %q", generated.Manifest.Artifacts[1].NativeVerification)
	}
	if !strings.Contains(string(generated.Checksums), "FlowRoutine-v1.2.3.spdx.json") {
		t.Fatalf("checksums do not contain SBOM: %s", generated.Checksums)
	}
	checksumDigest := sha256.Sum256(generated.Checksums)
	if generated.Manifest.Checksums.SHA256 != hex.EncodeToString(checksumDigest[:]) {
		t.Fatalf("checksum manifest digest does not match generated contents")
	}
	encoded, err := EncodeManifest(generated.Manifest)
	if err != nil {
		t.Fatalf("EncodeManifest() error = %v", err)
	}
	var decoded Manifest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("encoded manifest is invalid JSON: %v", err)
	}
	if decoded.SourceCommit != strings.Repeat("a", 40) {
		t.Fatalf("source commit = %q", decoded.SourceCommit)
	}
}

func TestGenerateRejectsIncompleteOrUnsafeInputs(t *testing.T) {
	tests := []struct {
		name        string
		change      func(*Options)
		omit        string
		wantMessage string
	}{
		{name: "invalid repository", change: func(options *Options) { options.Repository = "invalid" }, wantMessage: "owner/name"},
		{name: "invalid commit", change: func(options *Options) { options.SourceCommit = "main" }, wantMessage: "source commit"},
		{name: "invalid tag", change: func(options *Options) { options.Tag = "latest" }, wantMessage: "semantic version"},
		{name: "invalid prerelease", change: func(options *Options) { options.Tag = "v1.2.3-01" }, wantMessage: "semantic version"},
		{name: "zero sequence", change: func(options *Options) { options.ReleaseSequence = 0 }, wantMessage: "greater than zero"},
		{name: "missing time", change: func(options *Options) { options.PublishedAt = time.Time{} }, wantMessage: "published time"},
		{name: "missing platform", change: func(*Options) {}, omit: "windows-x64", wantMessage: "windows-x64"},
		{name: "missing SBOM", change: func(*Options) {}, omit: "sbom", wantMessage: "spdx.json"},
		{name: "unexpected file", change: func(*Options) {}, omit: "extra", wantMessage: "unexpected release asset"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			options := validOptions()
			test.change(&options)
			writeExpectedAssets(t, directory, options.Tag, test.omit)
			_, err := Generate(directory, options)
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("Generate() error = %v, want message containing %q", err, test.wantMessage)
			}
		})
	}
}

func validOptions() Options {
	return Options{
		Repository:      "Heee-oh/FlowRoutine",
		SourceCommit:    strings.Repeat("A", 40),
		Tag:             "v1.2.3",
		ReleaseSequence: 42,
		PublishedAt:     time.Date(2026, time.August, 1, 1, 2, 3, 0, time.FixedZone("KST", 9*60*60)),
	}
}

func writeExpectedAssets(t *testing.T, directory string, tag string, omit string) {
	t.Helper()
	for _, artifact := range expectedArtifacts {
		if artifact.platform == omit {
			continue
		}
		name := "FlowRoutine-" + tag + "-" + artifact.platform + artifact.extension
		writeTestFile(t, filepath.Join(directory, name), []byte("artifact:"+artifact.platform))
	}
	if omit != "sbom" {
		writeTestFile(t, filepath.Join(directory, "FlowRoutine-"+tag+".spdx.json"), []byte(`{"spdxVersion":"SPDX-2.3"}`))
	}
	if omit == "extra" {
		writeTestFile(t, filepath.Join(directory, "unreviewed.bin"), []byte("unexpected"))
	}
}

func writeTestFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
