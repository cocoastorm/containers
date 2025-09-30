# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Structure

This is a container images repository inspired by [home-operations/containers](https://github.com/home-operations/containers). The repository follows a structured approach for building and releasing container images using Docker Buildx and GitHub Actions.

### Directory Layout
- `apps/` - Contains individual container applications, each with its own Dockerfile and build configuration
- `include/` - Common files automatically copied to each application during build (contains shared .dockerignore)
- `.github/` - GitHub Actions workflows and reusable actions for CI/CD

### Application Structure
Each app in `apps/` follows this pattern:
- `Dockerfile` - Container build instructions
- `docker-bake.hcl` - Docker Buildx bake configuration defining build targets and metadata
- Optional: `scripts/` directory for application-specific scripts

## Build Commands

### Local Development
Use Task (Taskfile.yaml) for local development:

```bash
# List available tasks
task

# Build and test a specific app locally
task local-build-<app-name>
# Example: task local-build-mailhog
```

The local build process:
1. Copies shared files from `include/` to the app directory
2. Copies app-specific files
3. Builds using `docker buildx bake --no-cache --load`
4. Outputs the docker run command for testing

### Build Requirements
Each app must have:
- `docker-bake.hcl` file defining build configuration
- `Dockerfile` for the container build
- Dependencies: `docker`, `gh`, `jq`, `yq` must be available

## Docker Bake Configuration

Each app uses `docker-bake.hcl` with these standard targets:
- `image-local` - For local development (default group)
- `image` - Base target with metadata
- `image-all` - Multi-platform builds (linux/amd64, linux/arm64)

Standard variables in bake files:
- `APP` - Application name
- `VERSION` - Version tag (default: "latest")
- `SOURCE` - Upstream source repository URL

## GitHub Actions Workflow

The repository uses a reusable workflow system:
- `app-builder.yaml` - Main workflow for building applications
- `release.yaml` - Orchestrates releases across applications
- Custom actions in `.github/actions/` for app detection, versioning, and options

### Build Process
1. **Prepare** - Check app existence, get build options and versions
2. **Build** - Multi-platform builds with caching to GHCR
3. **Release** - Create multi-arch manifests and push to registry

### Registry
Images are published to GitHub Container Registry (GHCR) as:
`ghcr.io/<owner>/<app-name>:<version>`

## Current Applications
- `mailhog` - Email testing tool
- `mam-dynamic-ip` - Custom application with dynamic IP functionality