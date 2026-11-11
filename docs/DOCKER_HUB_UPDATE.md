---
title: "Docker Hub Description Update"
slug: "docs/docker-hub-update"
description: "Why the MDDB Docker Hub description is not refreshed automatically by CI, and how to update it by hand or enable the workflow step."
status: publish
---

# Docker Hub Description Update

The Docker Hub description is **not updated by CI**. `.github/workflows/release.yml`
(`docker-server`) builds and pushes the image and sets its labels, and nothing
in it writes the Hub listing: the `DOCKER_HUB_TOKEN` it holds is a push token
without the repository-write permission the description API wants. The texts
come from [LISTING.md](LISTING.md), which `server.json` and the guards in
`services/mddbd` share, so after each release the Hub is refreshed by hand from
two files that are already correct.

## Manual Update Process

### 1. Log in to Docker Hub
Go to: https://hub.docker.com/

### 2. Navigate to Repository
Go to: https://hub.docker.com/r/tradik/mddb

### 3. Update Full Description
1. Click on the repository
2. Go to the **"Overview"** tab
3. Click **"Edit"** button
4. Copy content from `docs/DOCKER_HUB.md` (its first paragraph is the long
   description from LISTING.md)
5. Paste into the description field
6. Click **"Update"**

### 4. Update Short Description
1. Go to **"Settings"** tab
2. Find **"Short Description"** field (100 characters at most)
3. Copy from `docs/DOCKER_HUB_SHORT.txt`, the one-liner from LISTING.md:
   ```
   Embedded knowledge base with 112 MCP tools, hybrid BM25+vector search and RAG. One Go binary.
   ```
4. Paste and save

### 5. Log it
Add a row to the submissions table in [LISTING.md](LISTING.md#submissions)
(Docker Hub, which text, the date, who).

## Automated Update (Optional)

To let the release refresh the description, the Docker Hub token needs more
than push:

### Requirements:
1. Go to: https://hub.docker.com/settings/security
2. Create a new Access Token with:
   - **Read & Write** permissions
   - **Repository** scope
3. Update GitHub Secret `DOCKER_HUB_TOKEN` with new token
4. Add the step below to the `docker-server` job in `.github/workflows/release.yml`,
   after "Build and push server image", with the action pinned by SHA as the
   other steps are

### Step for the Workflow:
```yaml
- name: Update Docker Hub description
  uses: peter-evans/dockerhub-description@<sha> # v5
  with:
    username: ${{ env.DOCKER_HUB_USERNAME }}
    password: ${{ secrets.DOCKER_HUB_TOKEN }}
    repository: ${{ env.DOCKER_HUB_USERNAME }}/${{ env.IMAGE_NAME }}
    readme-filepath: ./docs/DOCKER_HUB.md
    short-description: ${{ github.event.repository.description }}
```

Set the GitHub repository description to the same one-liner first
(`gh repo edit tradik/mddb --description "$(cat docs/DOCKER_HUB_SHORT.txt)"`),
so that the step reads it from there instead of a second copy in the workflow.
