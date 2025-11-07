# Docker Hub Setup Guide

This guide explains how to set up automated Docker image builds and pushes to Docker Hub.

## Prerequisites

1. **Docker Hub Account**: https://hub.docker.com/
2. **GitHub Repository**: Your MDDB repository
3. **Docker Hub Access Token**: For GitHub Actions

## Step 1: Create Docker Hub Repository

1. Log in to Docker Hub: https://hub.docker.com/
2. Click "Create Repository"
3. Repository details:
   - **Name**: `mddb`
   - **Description**: Use content from `docs/DOCKER_HUB_SHORT.txt`
   - **Visibility**: Public
4. Click "Create"

## Step 2: Generate Docker Hub Access Token

1. Go to Account Settings → Security: https://hub.docker.com/settings/security
2. Click "New Access Token"
3. Token details:
   - **Description**: `GitHub Actions - MDDB`
   - **Access permissions**: `Read, Write, Delete`
4. Click "Generate"
5. **Copy the token** (you won't see it again!)

## Step 3: Add GitHub Secret

1. Go to your GitHub repository
2. Navigate to: Settings → Secrets and variables → Actions
3. Click "New repository secret"
4. Secret details:
   - **Name**: `DOCKER_HUB_TOKEN`
   - **Value**: Paste the Docker Hub access token
5. Click "Add secret"

## Step 4: Update Docker Hub Description

1. Copy content from `docs/DOCKER_HUB.md`
2. Go to your Docker Hub repository: https://hub.docker.com/r/tradik/mddb
3. Click "Edit" on the Overview tab
4. Paste the content
5. Click "Update"

## Step 5: Verify Workflow

1. Push a tag to trigger the workflow:
   ```bash
   git tag -a v2.0.0 -m "Release v2.0.0"
   git push origin v2.0.0
   ```

2. Check GitHub Actions: https://github.com/tradik/mddb/actions

3. Verify Docker Hub: https://hub.docker.com/r/tradik/mddb/tags

## Available Images

After successful build, the following images will be available:

### Production Images
- `tradik/mddb:latest` - Latest stable release
- `tradik/mddb:2.0.0` - Specific version
- `tradik/mddb:2.0` - Minor version
- `tradik/mddb:2` - Major version

### Development Images
- `tradik/mddb:dev` - Latest development build
- `tradik/mddb:2.0.0-dev` - Specific version dev build

### Platforms
All images support:
- `linux/amd64` (x86_64)
- `linux/arm64` (aarch64)

## Testing Images

### Pull and run production image:
```bash
docker pull tradik/mddb:latest
docker run -d \
  --name mddb-test \
  -p 11023:11023 \
  -p 11024:11024 \
  -e MDDB_EXTREME=true \
  tradik/mddb:latest
```

### Test the server:
```bash
# Check health
curl http://localhost:11023/stats

# Add a document
curl -X POST http://localhost:11023/add \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "test",
    "key": "hello",
    "lang": "en",
    "content_md": "# Hello MDDB!"
  }'

# Get the document
curl http://localhost:11023/get/test/hello/en
```

### Clean up:
```bash
docker stop mddb-test
docker rm mddb-test
```

## Workflow Triggers

The Docker workflow runs on:

1. **Tag push**: `v*.*.*` (e.g., v2.0.0)
   - Builds and pushes versioned images
   - Updates `latest` tag
   - Updates Docker Hub description

2. **Main branch push**:
   - Builds and pushes `latest` tag
   - For testing purposes

3. **Manual trigger**:
   - Go to Actions → Docker Build and Push
   - Click "Run workflow"

## Troubleshooting

### Authentication Failed
- Verify `DOCKER_HUB_TOKEN` secret is set correctly
- Check token hasn't expired
- Ensure token has write permissions

### Build Failed
- Check GitHub Actions logs
- Verify Dockerfile exists and is valid
- Check if all dependencies are available

### Image Not Appearing
- Wait a few minutes for Docker Hub to update
- Check if workflow completed successfully
- Verify repository name matches

### Multi-platform Build Issues
- QEMU and Buildx are automatically set up
- Check if base images support both platforms
- Review build logs for platform-specific errors

## Updating Docker Hub Description

The description is automatically updated on each release. To manually update:

1. Edit `docs/DOCKER_HUB.md`
2. Commit and push changes
3. Trigger workflow (tag push or manual)

Or manually:
1. Go to Docker Hub repository
2. Click "Edit" on Overview tab
3. Paste content from `docs/DOCKER_HUB.md`
4. Click "Update"

## Security Best Practices

1. **Never commit tokens**: Use GitHub Secrets
2. **Rotate tokens regularly**: Every 6-12 months
3. **Use minimal permissions**: Read/Write only
4. **Monitor usage**: Check Docker Hub activity logs
5. **Revoke unused tokens**: Clean up old tokens

## Support

For issues:
- GitHub Actions logs: https://github.com/tradik/mddb/actions
- Docker Hub support: https://hub.docker.com/support
- MDDB issues: https://github.com/tradik/mddb/issues
