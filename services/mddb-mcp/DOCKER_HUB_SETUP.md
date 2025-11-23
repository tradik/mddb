# Docker Hub Setup for MCP

The MCP Docker image is now published to **both** registries:
- **GitHub Container Registry**: `ghcr.io/tradik/mddb/mddb-mcp:latest`
- **Docker Hub**: `tradik/mddb:mcp` (same repo as main MDDB server)

## Setup GitHub Secrets

To publish to Docker Hub, you need to add secrets to your GitHub repository:

### 1. Create Docker Hub Access Token

1. Go to https://hub.docker.com/settings/security
2. Click **New Access Token**
3. Name: `GitHub Actions MCP`
4. Permissions: **Read, Write, Delete**
5. Click **Generate**
6. **Copy the token** (you won't see it again!)

### 2. Add Secrets to GitHub

1. Go to https://github.com/tradik/mddb/settings/secrets/actions
2. Click **New repository secret**

**Add one secret:**

#### DOCKER_HUB_TOKEN
- Name: `DOCKER_HUB_TOKEN`
- Value: `dckr_pat_xxxxx...` (the token you copied)

**Note:** Username is hardcoded in workflow as `tradik` (see `env.DOCKER_HUB_USERNAME`)

### 3. Trigger Workflow

After adding secrets, push changes or manually trigger the workflow:

```bash
git add .
git commit -m "Add Docker Hub publishing for MCP"
git push
```

Or manually:
1. Go to https://github.com/tradik/mddb/actions/workflows/publish-mcp.yml
2. Click **Run workflow**
3. Select branch: `main`
4. Click **Run workflow**

## Verify Publication

### GitHub Container Registry
```bash
docker pull ghcr.io/tradik/mddb/mddb-mcp:latest
```

### Docker Hub
```bash
docker pull tradik/mddb:mcp
```

## Usage

Both images are identical. Use whichever you prefer:

### GitHub Container Registry (Public)
```bash
docker run -i --rm \
  --network host \
  -e MDDB_GRPC_ADDRESS=localhost:11024 \
  -e MDDB_REST_BASE_URL=http://localhost:11023 \
  ghcr.io/tradik/mddb/mddb-mcp:latest
```

### Docker Hub (Public)
```bash
docker run -i --rm \
  --network host \
  -e MDDB_GRPC_ADDRESS=localhost:11024 \
  -e MDDB_REST_BASE_URL=http://localhost:11023 \
  tradik/mddb:mcp
```

## Workflow Details

The workflow publishes to both registries automatically when:
- Push to `main` branch with changes in `services/mddb-mcp/**`
- Push tag `mcp-v*`
- Manual workflow dispatch

See `.github/workflows/publish-mcp.yml` for details.

## Troubleshooting

### "unauthorized: authentication required"
- Check if secrets are set correctly
- Verify Docker Hub token is valid
- Make sure token has **Write** permissions

### Image not appearing on Docker Hub
- Check workflow logs: https://github.com/tradik/mddb/actions
- Verify secrets names match exactly: `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`
- Wait a few minutes - Docker Hub can be slow to update

### "denied: requested access to the resource is denied"
- MCP uses the same repository as main MDDB server: `tradik/mddb`
- No need to create a separate repository
- Tags are: `tradik/mddb:mcp`, `tradik/mddb:mcp-latest`, etc.
