# Containers

The github actions (including actions and workflows) are a shameless copy of [home-operations/containers](https://github.com/home-operations/containers).

## List of container images:

- [mailhog](./apps/mailhog/)
- [qbittorrent-natpmp-sync](./apps/qbittorrent-natpmp-sync/)

## Go development

The repository pins Go and Task in `.mise.toml`. Run `mise install`, then use
`task go:fmt` to apply the repo-pinned `goimports` formatter and
`task go:check` to check formatting, run `go vet`, and run race-enabled tests.
The same checks run in CI for the Go app. Set `APP=<directory-name>` when a
future Go app is added under `apps/`.

## GitHub Actions checks

Run `task ci:lint` to check all workflows with actionlint. CI runs the same
check when workflows or local actions change. With Docker running, use
`task ci:go-local` to smoke-test the Go checks workflow through act. This
local run needs no GitHub token and does not run the image release workflow.
The act runner image differs from GitHub's hosted runner, so the real pull
request check remains the final confirmation.
