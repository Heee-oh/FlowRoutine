package releaseinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const ManifestSchemaVersion = 1

const (
	checksumFilename = "SHA256SUMS"
	oidcIssuer       = "https://token.actions.githubusercontent.com"
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	commitPattern     = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
	semverTagPattern  = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
)

type Options struct {
	Repository      string
	SourceCommit    string
	Tag             string
	ReleaseSequence uint64
	PublishedAt     time.Time
}

type Manifest struct {
	SchemaVersion   int         `json:"schemaVersion"`
	ReleaseSequence uint64      `json:"releaseSequence"`
	Version         string      `json:"version"`
	Tag             string      `json:"tag"`
	Repository      string      `json:"repository"`
	SourceCommit    string      `json:"sourceCommit"`
	PublishedAt     string      `json:"publishedAt"`
	OIDCIssuer      string      `json:"oidcIssuer"`
	SigningIdentity string      `json:"signingIdentity"`
	Artifacts       []Artifact  `json:"artifacts"`
	SBOM            ReleaseFile `json:"sbom"`
	Checksums       ReleaseFile `json:"checksums"`
}

type Artifact struct {
	Platform           string `json:"platform"`
	NativeVerification string `json:"nativeVerification"`
	ReleaseFile
}

type ReleaseFile struct {
	Name              string `json:"name"`
	URL               string `json:"url"`
	SizeBytes         int64  `json:"sizeBytes"`
	SHA256            string `json:"sha256"`
	SigstoreBundleURL string `json:"sigstoreBundleUrl"`
}

type GeneratedFiles struct {
	Manifest  Manifest
	Checksums []byte
}

type expectedArtifact struct {
	platform           string
	extension          string
	nativeVerification string
}

var expectedArtifacts = []expectedArtifact{
	{platform: "linux-x64", extension: ".tar.gz", nativeVerification: "sigstore"},
	{platform: "macos", extension: ".zip", nativeVerification: "apple-codesign-notarized+sigstore"},
	{platform: "windows-x64", extension: ".zip", nativeVerification: "authenticode+sigstore"},
}

func Generate(assetsDirectory string, options Options) (GeneratedFiles, error) {
	if err := validateOptions(options); err != nil {
		return GeneratedFiles{}, err
	}
	if err := validateInventory(assetsDirectory, options.Tag); err != nil {
		return GeneratedFiles{}, err
	}

	artifacts := make([]Artifact, 0, len(expectedArtifacts))
	checksumSubjects := make([]ReleaseFile, 0, len(expectedArtifacts)+1)
	for _, expected := range expectedArtifacts {
		name := fmt.Sprintf("FlowRoutine-%s-%s%s", options.Tag, expected.platform, expected.extension)
		file, err := describeFile(assetsDirectory, name, options)
		if err != nil {
			return GeneratedFiles{}, fmt.Errorf("%s artifact: %w", expected.platform, err)
		}
		artifacts = append(artifacts, Artifact{
			Platform:           expected.platform,
			NativeVerification: expected.nativeVerification,
			ReleaseFile:        file,
		})
		checksumSubjects = append(checksumSubjects, file)
	}

	sbomName := fmt.Sprintf("FlowRoutine-%s.spdx.json", options.Tag)
	sbom, err := describeFile(assetsDirectory, sbomName, options)
	if err != nil {
		return GeneratedFiles{}, fmt.Errorf("SBOM: %w", err)
	}
	checksumSubjects = append(checksumSubjects, sbom)
	checksums := encodeChecksums(checksumSubjects)
	checksumFile := describeBytes(checksumFilename, checksums, options)

	identity := fmt.Sprintf(
		"https://github.com/%s/.github/workflows/release.yml@refs/tags/%s",
		options.Repository,
		options.Tag,
	)
	return GeneratedFiles{
		Manifest: Manifest{
			SchemaVersion:   ManifestSchemaVersion,
			ReleaseSequence: options.ReleaseSequence,
			Version:         strings.TrimPrefix(options.Tag, "v"),
			Tag:             options.Tag,
			Repository:      options.Repository,
			SourceCommit:    strings.ToLower(options.SourceCommit),
			PublishedAt:     options.PublishedAt.UTC().Format(time.RFC3339),
			OIDCIssuer:      oidcIssuer,
			SigningIdentity: identity,
			Artifacts:       artifacts,
			SBOM:            sbom,
			Checksums:       checksumFile,
		},
		Checksums: checksums,
	}, nil
}

func validateInventory(directory string, tag string) error {
	allowed := map[string]struct{}{
		fmt.Sprintf("FlowRoutine-%s.spdx.json", tag): {},
	}
	for _, expected := range expectedArtifacts {
		allowed[fmt.Sprintf("FlowRoutine-%s-%s%s", tag, expected.platform, expected.extension)] = struct{}{}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read release assets: %w", err)
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok {
			return fmt.Errorf("unexpected release asset %q", entry.Name())
		}
		delete(allowed, entry.Name())
	}
	if len(allowed) > 0 {
		missing := make([]string, 0, len(allowed))
		for name := range allowed {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return fmt.Errorf("missing release assets: %s", strings.Join(missing, ", "))
	}
	return nil
}

func EncodeManifest(manifest Manifest) ([]byte, error) {
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode update manifest: %w", err)
	}
	return append(encoded, '\n'), nil
}

func validateOptions(options Options) error {
	if !repositoryPattern.MatchString(options.Repository) {
		return fmt.Errorf("repository must use owner/name syntax")
	}
	if !commitPattern.MatchString(options.SourceCommit) {
		return fmt.Errorf("source commit must be a 40-64 character hexadecimal digest")
	}
	if !validSemverTag(options.Tag) {
		return fmt.Errorf("tag must be a semantic version prefixed with v")
	}
	if options.ReleaseSequence == 0 {
		return fmt.Errorf("release sequence must be greater than zero")
	}
	if options.PublishedAt.IsZero() {
		return fmt.Errorf("published time is required")
	}
	return nil
}

func validSemverTag(tag string) bool {
	matches := semverTagPattern.FindStringSubmatch(tag)
	if matches == nil {
		return false
	}
	for _, identifier := range strings.Split(matches[4], ".") {
		if len(identifier) > 1 && identifier[0] == '0' && allDigits(identifier) {
			return false
		}
	}
	return true
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func describeFile(directory string, name string, options Options) (ReleaseFile, error) {
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return ReleaseFile{}, fmt.Errorf("inspect %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return ReleaseFile{}, fmt.Errorf("%q must be a regular file", name)
	}
	if info.Size() == 0 {
		return ReleaseFile{}, fmt.Errorf("%q must not be empty", name)
	}

	file, err := os.Open(path)
	if err != nil {
		return ReleaseFile{}, fmt.Errorf("open %q: %w", name, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ReleaseFile{}, fmt.Errorf("hash %q: %w", name, err)
	}
	return releaseFile(name, info.Size(), hex.EncodeToString(hash.Sum(nil)), options), nil
}

func describeBytes(name string, contents []byte, options Options) ReleaseFile {
	hash := sha256.Sum256(contents)
	return releaseFile(name, int64(len(contents)), hex.EncodeToString(hash[:]), options)
}

func releaseFile(name string, size int64, digest string, options Options) ReleaseFile {
	url := fmt.Sprintf(
		"https://github.com/%s/releases/download/%s/%s",
		options.Repository,
		options.Tag,
		name,
	)
	return ReleaseFile{
		Name:              name,
		URL:               url,
		SizeBytes:         size,
		SHA256:            digest,
		SigstoreBundleURL: url + ".sigstore.json",
	}
}

func encodeChecksums(files []ReleaseFile) []byte {
	ordered := append([]ReleaseFile(nil), files...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Name < ordered[right].Name
	})
	var checksums strings.Builder
	for _, file := range ordered {
		fmt.Fprintf(&checksums, "%s  %s\n", file.SHA256, file.Name)
	}
	return []byte(checksums.String())
}
