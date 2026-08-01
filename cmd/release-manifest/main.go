package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"flowroutine/internal/releaseinfo"
)

func main() {
	assetsDirectory := flag.String("assets", "release-assets", "directory containing packaged release artifacts and the SBOM")
	outputPath := flag.String("output", "release-assets/update-manifest.json", "signed update manifest output path")
	repository := flag.String("repository", "", "GitHub repository in owner/name format")
	sourceCommit := flag.String("source-commit", "", "source commit digest")
	tag := flag.String("tag", "", "semantic release tag prefixed with v")
	releaseSequence := flag.Uint64("sequence", 0, "monotonically increasing GitHub Actions run number")
	flag.Parse()

	generated, err := releaseinfo.Generate(*assetsDirectory, releaseinfo.Options{
		Repository:      *repository,
		SourceCommit:    *sourceCommit,
		Tag:             *tag,
		ReleaseSequence: *releaseSequence,
		PublishedAt:     time.Now().UTC(),
	})
	if err != nil {
		fail(err)
	}
	manifest, err := releaseinfo.EncodeManifest(generated.Manifest)
	if err != nil {
		fail(err)
	}
	if err := writeAtomic(filepath.Join(*assetsDirectory, "SHA256SUMS"), generated.Checksums); err != nil {
		fail(err)
	}
	if err := writeAtomic(*outputPath, manifest); err != nil {
		fail(err)
	}
	fmt.Printf("generated %s and %s\n", filepath.Join(*assetsDirectory, "SHA256SUMS"), *outputPath)
}

func writeAtomic(path string, contents []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".release-metadata-*")
	if err != nil {
		return fmt.Errorf("create temporary metadata file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set metadata permissions: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write metadata: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync metadata: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close metadata: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish metadata: %w", err)
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
