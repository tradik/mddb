# Docker Hub Description Update

The Docker Hub description is **not automatically updated** by GitHub Actions due to token permission requirements.

## Manual Update Process

### 1. Log in to Docker Hub
Go to: https://hub.docker.com/

### 2. Navigate to Repository
Go to: https://hub.docker.com/r/tradik/mddb

### 3. Update Full Description
1. Click on the repository
2. Go to the **"Overview"** tab
3. Click **"Edit"** button
4. Copy content from `docs/DOCKER_HUB.md`
5. Paste into the description field
6. Click **"Update"**

### 4. Update Short Description
1. Go to **"Settings"** tab
2. Find **"Short Description"** field
3. Copy from `docs/DOCKER_HUB_SHORT.txt`:
   ```
   High-performance markdown database with dual protocol support (HTTP/JSON + gRPC/Protobuf) and full revision history
   ```
4. Paste and save

## Automated Update (Optional)

To enable automated description updates, the Docker Hub token needs additional permissions:

### Requirements:
1. Go to: https://hub.docker.com/settings/security
2. Create a new Access Token with:
   - **Read & Write** permissions
   - **Repository** scope
3. Update GitHub Secret `DOCKER_HUB_TOKEN` with new token
4. Uncomment the "Update Docker Hub description" step in `.github/workflows/docker.yml`

### Enable in Workflow:
```yaml
# Uncomment these lines in .github/workflows/docker.yml
- name: Update Docker Hub description
  uses: peter-evans/dockerhub-description@v5
  with:
    username: ${{ env.DOCKER_HUB_USERNAME }}
    password: ${{ secrets.DOCKER_HUB_TOKEN }}
    repository: ${{ env.DOCKER_HUB_USERNAME }}/${{ env.IMAGE_NAME }}
    readme-filepath: ./docs/DOCKER_HUB.md
    short-description: "High-performance markdown database with dual protocol support (HTTP/JSON + gRPC/Protobuf) and full revision history"
```

## Files to Update

When updating Docker Hub description, make sure these files are current:

- **`docs/DOCKER_HUB.md`** - Full description (markdown)
- **`docs/DOCKER_HUB_SHORT.txt`** - Short description (100 chars max)

## Update Frequency

Update the Docker Hub description when:
- ✅ New major version released
- ✅ Performance benchmarks change significantly
- ✅ New features added
- ✅ Platform support changes
- ✅ Documentation improvements

## Current Status

- **Automated Updates**: ❌ Disabled (token permissions)
- **Manual Updates**: ✅ Required
- **Last Updated**: Check Docker Hub repository
