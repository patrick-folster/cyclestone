# Release Checklist

## Before Tagging

- Confirm `README.md`, `SECURITY.md`, `CHANGELOG.md`, `CONTRIBUTING.md`, and GitHub templates are current.
- Confirm GitHub private vulnerability reporting is enabled or another private security intake is documented in `SECURITY.md`.
- Confirm `go test ./...` passes.
- Confirm `go vet ./...` passes.
- Confirm `gofmt -l .` returns no files.
- Confirm `.goreleaser.yml` contains the intended platforms.
- Confirm no credentials or local `.cyclestone` runtime files are committed.

## Tag Format

Use semantic version tags:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow runs for tags matching `v*`.

## GoReleaser Output

The release workflow builds:

- Linux amd64/arm64
- macOS amd64/arm64
- Windows amd64/arm64

Expected artifacts:

- Platform archives.
- `checksums.txt`.
- GitHub Release notes generated from commit history.

## Checksum Verification

After downloading a release archive, verify it against `checksums.txt`.

```bash
sha256sum -c checksums.txt
```

On macOS, use:

```bash
shasum -a 256 -c checksums.txt
```

## Release Notes

Use `CHANGELOG.md` as the source of human-readable release notes. Include:

- New features.
- Security-relevant changes.
- Breaking changes.
- Known limitations.
- Upgrade notes.
