# Release Process

This document describes the release process for MDDB.

## Prerequisites

- Push access to the repository
- GitHub CLI (`gh`) installed (optional)
- Git configured with your credentials

## Release Steps

### 1. Update Version

Update version in relevant files:
- `CHANGELOG.md` - Move unreleased changes to new version section
- `README.md` - Update version numbers if needed
- Any other version references

### 2. Commit Changes

```bash
git add .
git commit -m "Release v2.0.0"
git push origin main
```

### 3. Create and Push Tag

```bash
# Create annotated tag
git tag -a v2.0.0 -m "Release v2.0.0 - Ultra Performance"

# Push tag to trigger release workflow
git push origin v2.0.0
```

### 4. Build Docker Images (Optional)

For Docker releases, build the panel image:

```bash
# Build panel Docker image
make docker-build-panel

# Or build all images (server + panel)
make docker-build-all

# Tag with version
docker tag mddb-panel:latest mddb-panel:2.0.3
docker tag mddb:latest mddb:2.0.3

# Push to registry (if configured)
docker push mddb-panel:2.0.3
docker push mddb:2.0.3
```

### 5. Monitor GitHub Actions

The release workflow will automatically:
1. Build binaries for all platforms (Linux, macOS, FreeBSD)
2. Create DEB packages for Ubuntu/Debian
3. Create RPM packages for RHEL/CentOS/Fedora
4. Create tarballs for all platforms
5. Generate Homebrew formulas
6. Create GitHub Release with all artifacts

Check progress at: https://github.com/tradik/mddb/actions

### 6. Verify Release

Once the workflow completes:

1. Check GitHub Releases page
2. Verify all artifacts are present:
   - `mddbd-v2.0.0-linux-amd64.deb`
   - `mddbd-v2.0.0-linux-amd64.rpm`
   - `mddbd-v2.0.0-linux-arm64.deb`
   - `mddbd-v2.0.0-linux-arm64.rpm`
   - `mddbd-v2.0.0-darwin-amd64.tar.gz`
   - `mddbd-v2.0.0-darwin-arm64.tar.gz`
   - `mddbd-v2.0.0-freebsd-amd64.tar.gz`
   - Same for `mddb-cli`
3. Test Docker images (if built):
   ```bash
   docker pull mddb:2.0.3
   docker pull mddb-panel:2.0.3
   docker run --rm mddb:2.0.3 --version
   docker run --rm mddb-panel:2.0.3 --help
   ```
4. Test installation on at least one platform

### 7. Update Homebrew Tap (Optional)

If you maintain a Homebrew tap:

```bash
# Clone your tap repository
git clone https://github.com/tradik/homebrew-mddb.git
cd homebrew-mddb

# Download the Homebrew formulas from release artifacts
# Update SHA256 checksums
shasum -a 256 mddbd-v2.0.0-darwin-amd64.tar.gz
shasum -a 256 mddbd-v2.0.0-darwin-arm64.tar.gz

# Update formulas with correct SHA256
# Commit and push
git add .
git commit -m "Update to v2.0.0"
git push origin main
```

### 8. Announce Release

- Update project website (if any)
- Post on social media
- Notify users on mailing list/Discord/Slack
- Update documentation site

## Manual Release (if needed)

If GitHub Actions fails or you need to release manually:

```bash
# Build for all platforms
make build-all-platforms

# Create packages
make create-packages

# Create release
gh release create v2.0.0 \
  --title "MDDB v2.0.0 - Ultra Performance" \
  --notes-file release-notes.md \
  dist/*.deb \
  dist/*.rpm \
  dist/*.tar.gz
```

## Hotfix Release

For urgent fixes:

```bash
# Create hotfix branch from tag
git checkout -b hotfix/v2.0.1 v2.0.0

# Make fixes
git add .
git commit -m "Fix critical bug"

# Merge to main
git checkout main
git merge hotfix/v2.0.1

# Tag and release
git tag -a v2.0.1 -m "Hotfix v2.0.1"
git push origin main v2.0.1
```

## Rollback

If a release has critical issues:

```bash
# Delete tag locally and remotely
git tag -d v2.0.0
git push origin :refs/tags/v2.0.0

# Delete GitHub release
gh release delete v2.0.0

# Fix issues and re-release
```

## Version Numbering

MDDB follows [Semantic Versioning](https://semver.org/):

- **MAJOR** (v2.0.0): Breaking API changes
- **MINOR** (v2.1.0): New features, backward compatible
- **PATCH** (v2.0.1): Bug fixes, backward compatible

## Checklist

Before releasing:

- [ ] All tests pass
- [ ] Documentation is updated
- [ ] CHANGELOG.md is updated
- [ ] Version numbers are correct
- [ ] Performance benchmarks are current
- [ ] Breaking changes are documented
- [ ] Migration guide exists (if needed)
- [ ] Security issues are addressed

After releasing:

- [ ] Release artifacts are verified
- [ ] Installation works on target platforms
- [ ] Documentation site is updated
- [ ] Announcement is published
- [ ] GitHub release is public
- [ ] Homebrew formula is updated (if applicable)

## Support

For questions about the release process:
- Open an issue on GitHub
- Contact maintainers directly
- Check GitHub Actions logs for errors
