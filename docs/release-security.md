# Release security

FlowRoutine's tag workflow fails closed: a release is published only after all platform packages, native signatures,
checksums, the SBOM, update metadata, Sigstore bundles, and provenance attestations succeed.

## Protected environments

Create these GitHub environments before publishing a tag. Restrict each environment to `v*.*.*` tags, require an
independent reviewer, prevent self-review, and limit repository administration of environment secrets.

| Environment | Purpose | Secrets |
| --- | --- | --- |
| `release-build` | Linux package build; no signing key access | None |
| `release-macos-signing` | Developer ID signing and Apple notarization | `MACOS_CERTIFICATE_P12_BASE64`, `MACOS_CERTIFICATE_PASSWORD`, `MACOS_SIGNING_IDENTITY`, `MACOS_NOTARY_KEY_P8_BASE64`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_ISSUER_ID` |
| `release-windows-signing` | Windows Authenticode signing | `WINDOWS_CERTIFICATE_PFX_BASE64`, `WINDOWS_CERTIFICATE_PASSWORD` |
| `release-publishing` | OIDC signing, attestation, and GitHub Release publication | None; GitHub issues a short-lived OIDC token |

Use a Developer ID Application certificate for macOS and a code-signing certificate trusted by Windows. Store only
base64-encoded certificate containers in GitHub, never in the repository. The notarization key should be a dedicated
App Store Connect API key with only the access required by `notarytool`. Rotate certificates before expiry, revoke
replaced credentials, and test a release candidate tag after every rotation.

The workflow imports certificates into ephemeral runner stores, verifies the native signature, and removes imported
material before packaging. Missing or invalid signing material stops the release. Only the publication job receives
`contents: write`, `attestations: write`, and `id-token: write`; build jobs retain read-only repository access. Every
third-party action is pinned to an immutable commit SHA.

## Published verification material

Each release includes:

- signed and packaged Linux, macOS, and Windows applications;
- `SHA256SUMS` for all application archives and the SPDX JSON SBOM;
- `FlowRoutine-<tag>.spdx.json`;
- `update-manifest.json`, including the source commit, release sequence, exact URLs, sizes, and SHA-256 digests;
- one `<file>.sigstore.json` bundle for every published non-bundle file; and
- GitHub build-provenance attestations for the files listed in `SHA256SUMS`.

The expected keyless signing identity for tag `<tag>` is:

```text
https://github.com/Heee-oh/FlowRoutine/.github/workflows/release.yml@refs/tags/<tag>
```

The expected OIDC issuer is `https://token.actions.githubusercontent.com`. Consumers must compile or configure
these trust values independently; never trust identity or issuer fields merely because they appear inside the
manifest being verified.

## Verify a release

Download the desired release files and their `.sigstore.json` bundles. With Cosign 3 installed, verify each file
before opening an archive:

```bash
tag=v1.2.3
file="FlowRoutine-${tag}-linux-x64.tar.gz"
identity="https://github.com/Heee-oh/FlowRoutine/.github/workflows/release.yml@refs/tags/${tag}"

cosign verify-blob \
  --bundle "${file}.sigstore.json" \
  --certificate-identity "${identity}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "${file}"
```

Verify `SHA256SUMS` with the same command, then verify its contents from the download directory:

```bash
sha256sum -c SHA256SUMS
# macOS: shasum -a 256 -c SHA256SUMS
```

After extraction, perform the platform-native checks as well:

```bash
# macOS
codesign --verify --deep --strict FlowRoutine.app
spctl --assess --type execute FlowRoutine.app
xcrun stapler validate FlowRoutine.app
```

```powershell
# Windows; require Status = Valid
Get-AuthenticodeSignature .\FlowRoutine.exe
```

GitHub provenance can be checked with `gh attestation verify <artifact> --repo Heee-oh/FlowRoutine`.

## Opt-in update protocol

Automatic background updates remain disabled. A future desktop or headless update client must require an explicit
user opt-in and apply this sequence:

1. Fetch `update-manifest.json` and its bundle from the latest release of the canonical repository over HTTPS.
2. Verify the raw manifest bytes against the compiled workflow identity and OIDC issuer before using any manifest URL.
3. Require schema version `1`, a semantic version greater than the running version, and a `releaseSequence` greater
   than the highest sequence previously accepted on that installation.
4. Persist the highest accepted sequence in application-owned storage. A lower or equal sequence or version is a
   replay/downgrade and must fail without offering an override.
5. Select only the exact supported platform entry, download to a temporary file, and verify its size, SHA-256 digest,
   Sigstore bundle, and platform-native signature before prompting the user to install it.
6. Require final user confirmation, replace atomically where the platform permits, and never send runtime secrets,
   scenarios, or telemetry during update checks.

The signed, monotonically increasing workflow run number provides replay ordering independent of semantic-version
formatting. A manual downgrade remains possible only outside the updater and must be an explicit operator action.
