---
name: epinio-cli
description: Manage Epinio with the epinio CLI instead of MCP — apps, push, namespaces, env, configurations, services, catalog, appcharts, builder images, gitconfigs, logs. Use when MCP is unavailable or disallowed, when deploying or managing Epinio from the terminal, or when replacing epinio-mcp tool calls with CLI commands.
---

# Epinio CLI (MCP stand-in)

Run `epinio` commands. Do not use MCP tools, do not invent REST endpoints except the one source-tarball fallback below, do not invent flags.

Unknown syntax: `epinio <cmd> --help`.

## Preconditions

1. `epinio` is on PATH. If missing, stop and say so.
2. Prefer CLI **1.14.1+** (same floor as epinio-mcp). If `epinio version` is older than the server in `epinio info -o json`, run `epinio client-sync`. `app chart create/update/delete` and all `buildimage` commands need 1.14.1+.
3. Session works: `epinio info -o json`. On auth/TLS failure: `epinio login <URL>` (`--user`/`--password`, or `--oidc`). Self-signed: `--trust-ca` or `--skip-ssl-verification`.
4. Namespaced commands use the **targeted** namespace, not a `--namespace` flag. Always:

```bash
epinio target <namespace>
```

Default namespace is `workspace`. Confirm with `epinio target` (no args) or `epinio settings show`.

Prefer `-o json` on commands that accept it: `info`, `app list`, `app show`, `namespace list`, `namespace show`, `configuration list`, `configuration show`, `service list`, `service show`.

Destructive deletes: use the exact name the user gave, or confirm from a prior `list`/`show`. Namespace delete prompts unless `-f` is passed.

## MCP tool → CLI

Placeholders: `NS` namespace, `APP` app, `NAME` resource name.

### Info / namespaces

| MCP | CLI |
|---|---|
| `epinio_info` | `epinio info -o json` |
| `list_namespaces` | `epinio namespace list -o json` |
| `create_namespace` | `epinio namespace create NAME` |
| `delete_namespace` | `epinio namespace delete NAME -f` |

### Apps

| MCP | CLI |
|---|---|
| `list_apps` (one NS) | `epinio target NS` then `epinio app list -o json` |
| `list_apps` (all NS) | `epinio app list --all -o json` |
| `show_app` | `epinio target NS` then `epinio app show APP -o json` |
| `create_app` | `epinio app create APP [--instances N] [--app-chart CHART] [--env KEY=VALUE] [--bind CONFIG] [--route HOST]` |
| `delete_app` | `epinio app delete APP` (more names: `APP1 APP2`; `--all` only if the user asked) |
| `restart_app` | `epinio app restart APP` |
| `scale_app` | `epinio app update APP --instances N` |
| `update_app` | `epinio app update APP` with only the flags that change: `--instances`, `--env KEY=VALUE`, `--env-replace`, `--bind`, `--route`, `--clear-routes`, `--app-chart`, `--chart-value KEY=VALUE`, `--no-restart` |
| `get_app_manifest` | `epinio app show APP -o json` **or** `epinio app manifest APP ./epinio.yml` |

`update_app` omitted fields stay unchanged. `--bind` / `--route` replace those lists only when you pass at least one value; empty flags are ignored. Clear routes with `--clear-routes`. `--no-restart` skips the pod roll (`update_app` `restart=false`).

### Push / stage / deploy

| MCP | CLI |
|---|---|
| `push_app` | `epinio push --name APP --path DIR [--builder-image IMAGE] [--app-chart CHART]` |
| `upload_and_stage` + `deploy_staged` | No two-step CLI. Use `epinio push` (upload + stage + deploy). Rebuild existing: `epinio app restage APP` (`--no-restart` to skip restart). |
| Git origin (public) | `epinio push --name APP --git URL,REVISION [--git-provider PROVIDER]` |
| Git origin (private) | Create a gitconfig first, then push with an explicit `origin.git.gitconfig` (see Gitconfigs). |
| Prebuilt image | `epinio push --name APP --container-image-url IMAGE` |

`push` with no args reads `./epinio.yml`. App name: lowercase, alphanumeric + hyphens.

Also valid: `--env KEY=VALUE`, `--bind CONFIG`, `--instances N`, `--route HOST`, `--chart-value KEY=VALUE`.

`--git-provider` values: check `epinio push --help`. Typical: `git`, `github`, `github_enterprise`, `gitlab`, `gitlab_enterprise`.

### Env

| MCP | CLI |
|---|---|
| `list_env` | `epinio app env list APP` |
| `set_env` | `epinio app env set APP KEY VALUE` (repeat per key) |
| `unset_env` | `epinio app env unset APP KEY` |

### Configurations

| MCP | CLI |
|---|---|
| `list_configurations` | `epinio configuration list -o json` (`--all` for every namespace) |
| `create_configuration` | `epinio configuration create NAME KEY=VALUE [KEY=VALUE ...]` |
| `delete_configuration` | `epinio configuration delete NAME` (`--unbind` if bound) |
| `bind_configuration` | `epinio configuration bind CONFIG APP` |
| `unbind_configuration` | `epinio configuration unbind CONFIG APP` |

### Services / catalog

| MCP | CLI |
|---|---|
| `list_services` | `epinio service list -o json` (`--all` for every namespace) |
| `list_catalog_services` | `epinio service catalog` |
| `show_catalog_service` | `epinio service catalog NAME` |
| `create_service` | `epinio service create CATALOG_NAME INSTANCE_NAME [--chart-value KEY=VALUE] [--wait]` |
| `delete_service` | `epinio service delete NAME` |
| `bind_service` | `epinio service bind SERVICE APP` |
| `unbind_service` | `epinio service unbind SERVICE APP` |
| `create_catalog_service` | `epinio service catalog create --name NAME --chart CHART [--chart-version V] [--app-version V] [--description TEXT] [--short-description TEXT] [--helm-repo-name N] [--helm-repo-url URL] [--helm-repo-secret S] [--values-file FILE] [--secret-types T] [--service-icon URL]` |
| `update_catalog_service` | `epinio service catalog update NAME` + the same flags except `--name` |
| `delete_catalog_service` | `epinio service catalog delete NAME` |

### Appcharts / builder images

| MCP | CLI |
|---|---|
| `list_appcharts` | `epinio app chart list` |
| `show_appchart` | `epinio app chart show NAME` |
| `create_appchart` | `epinio app chart create --name NAME [--helm-chart URL] [--helm-repo URL] [--description TEXT] [--short-description TEXT] [--set KEY=VALUE]` |
| `update_appchart` | `epinio app chart update NAME` + the same flags except `--name` |
| `delete_appchart` | `epinio app chart delete NAME` |
| `list_builder_images` | `epinio buildimage list` |
| `show_builder_image` | `epinio buildimage show NAME` |
| `create_builder_image` | `epinio buildimage create --name NAME --image IMAGE [--description TEXT] [--short-description TEXT]` |
| `update_builder_image` | `epinio buildimage update NAME [--image IMAGE] [--description TEXT] [--short-description TEXT]` |
| `delete_builder_image` | `epinio buildimage delete NAME` |

If `buildimage` or `app chart create` is unknown, the CLI is too old — `epinio client-sync`.

### Gitconfigs

Credentials Epinio uses to clone a private git repo. No update command (MCP has none either): delete and recreate. Passwords and certificates are write-only — list/show never return them.

| MCP | CLI |
|---|---|
| `list_gitconfigs` | `epinio gitconfig list` |
| `show_gitconfig` | `epinio gitconfig show NAME` |
| `match_gitconfigs` | No match command. `epinio gitconfig list`, keep ids whose name starts with the prefix. Empty prefix = every id. |
| `create_gitconfig` | `epinio gitconfig create NAME URL [--git-provider PROVIDER] [--username USER] [--password PASS] [--user-org ORG] [--repository REPO] [--skip-ssl] [--cert-file FILE] [--global]` |
| `delete_gitconfig` | `epinio gitconfig delete NAME` (one id per call). `epinio gitconfig delete --all` only after the user confirms. |

`--git-provider`: use whatever `epinio gitconfig create --help` lists. Typical: `git`, `github`, `github_enterprise`, `gitlab`, `gitlab_enterprise`. MCP's `github_enterprise_cloud` / `github_enterprise_self_hosted` map to `github_enterprise` unless `--help` lists the finer names.

`--global` is admin-only (Epinio ≥ 1.14.1). Skip the flag if `--help` does not show it.

MCP `certificate` is a PEM string. Write it to a temp file and pass `--cert-file`. Never log `--password`.

**Private git push (Epinio ≥ 1.14.1):** selection is explicit — URL matching is not enough. After `gitconfig create`, push from a manifest that sets `origin.git.gitconfig`:

```yaml
name: APP
origin:
  git:
    url: https://github.com/org/repo
    revision: main
    gitconfig: NAME
```

```bash
epinio push ./epinio.yml
```

`--git URL,REVISION` alone clones unauthenticated. If push fails with `authentication required`, add `gitconfig` to the manifest. Confirm the key with `epinio push --help` / docs if the CLI rejects it.

### Logs

| MCP | CLI |
|---|---|
| `app_logs` (runtime) | `epinio app logs APP` |
| `app_logs` (staging) | `epinio app logs APP --staging` |
| `get_connection_info` | No WS URL from CLI. Stream with `epinio app logs APP --follow` (add `--staging` for the last build). |

`--follow` is a long-running stream. Use it only when the user asked to tail. Otherwise omit it.

### Clone

No `clone` command (`clone_app`). Recreate from the source image (no rebuild):

```bash
epinio target SRC_NS
epinio app show SRC -o json
epinio target DST_NS
epinio app create DST --instances N --app-chart CHART
epinio app env set DST KEY VALUE   # each copied env var
epinio push --name DST --container-image-url IMAGE_URL
```

Copy env and chart from the show JSON. New routes are assigned automatically.

### Source files

No CLI for `get_app_source` / `list_app_files`. `epinio app export` is Helm values + chart + image, **not** the staging tarball.

Tarball (Epinio ≥ 1.14.1): `epinio settings show` for API Url; token from the settings file path that command prints (`token.accesstoken`, else basic `user`/`pass`).

```bash
curl -fsS -k -H "Authorization: Bearer TOKEN" \
  "$API/api/v1/namespaces/$NS/applications/$APP/source" -o source.tgz
tar -tzf source.tgz          # list_app_files
tar -xzf source.tgz -C DIR   # get_app_source extract
```

If that GET fails, say source retrieval is unavailable via CLI.

### Guidance

`get_build_guidance` has no CLI. Read [guidance.md](guidance.md).

## Gaps (do not fake)

Elevated MCP tools have **no** CLI: `adopt_app`, `reconcile_app`, `release_app`, `check_capabilities`, `enable_capability`. Do not invent kubectl stand-ins unless the user asks for Kubernetes work.

No `update_gitconfig`. No `match_gitconfigs` command.

## Workflows

**Deploy from a local directory**

1. `epinio namespace list -o json` — create NS if needed
2. `epinio target NS`
3. `epinio buildimage list` if a non-default builder is required
4. `epinio push --name APP --path DIR`
5. `epinio app show APP -o json`

**Deploy from a private git repo**

1. `epinio gitconfig list` — create one if needed (`epinio gitconfig create …`)
2. `epinio target NS`
3. Write a manifest with `origin.git.url`, `revision`, and `gitconfig`
4. `epinio push ./epinio.yml`
5. `epinio app show APP -o json`

**Failed staging**

1. `epinio app show APP -o json` — read `stagingstatus` / `stage_id`
2. `epinio app logs APP --staging`
3. Fix source (see [guidance.md](guidance.md)), then `epinio push --name APP --path DIR` or `epinio app restage APP`

**Do not** wrap source in an extra `app/` directory. `package.json` / `go.mod` / `requirements.txt` must be at the path root passed to `--path`. Bind to `0.0.0.0:$PORT`.
