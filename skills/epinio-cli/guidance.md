# Deploy / staging guidance

Use with the Epinio CLI. Live cluster data comes from `epinio app chart list`, `epinio app chart show NAME`, `epinio buildimage list`, `epinio info -o json` — do not hard-code names.

## Deploy

Paketo buildpacks detect from the **root** of `--path` (extracted to `/workspace/source/app` in the builder).

- Language files (`package.json`, `go.mod`, `requirements.txt`) at that root, not nested under an extra `app/` folder.
- Listen on `$PORT` (`0.0.0.0`, fallback 8080). Binding `127.0.0.1` fails readiness.
- App name: lowercase, alphanumeric + hyphens.
- Namespace: `epinio target NS` (default `workspace`).

**Node:** `package.json` at root with a `"start"` script. Bind `process.env.PORT || 8080`.

**Next.js 16+:** `"build": "next build --webpack"`. Turbopack production builds fail in Paketo with `TurbopackInternalError: Symlink node_modules is invalid`. Standalone: copy `.next/static` and `public/` into `.next/standalone/`, launch `HOSTNAME=0.0.0.0 node .next/standalone/server.js`.

**Go:** `go.mod` at root. Bind `:$PORT`.

**Python:** `requirements.txt` or `pyproject.toml` at root. Prefer a `Procfile` (`web: gunicorn app:app`). Bind `0.0.0.0:$PORT`.

## Private git

Create credentials with `epinio gitconfig create` before pushing a private repo. Epinio ≥ 1.14.1 requires an explicit `origin.git.gitconfig` on the app — it does not pick a gitconfig by URL. PAT goes in `--password`; `--username` must be non-empty.

## Appchart

`epinio app chart list` / `show NAME`. Pass `--app-chart` on create/push/update. Settings: `--chart-value KEY=VALUE` only for keys in that chart's schema.

- **standard** — default; no extra K8s API access.
- **standard-elevated** — extra RBAC for apps that must call the Kubernetes API. Do not pick “to be safe”.
- **rancher-extension** — Rancher UI extensions; settings `extName`, `extVersion`.

## Builder image

`epinio buildimage list`. Pass `--builder-image` on push. Cluster default is in `epinio info -o json` (`default_builder_image`) and the list entry marked default.

Typical default: `paketobuildpacks/builder-jammy-full`. Use a specialized builder only when the chart/app requires it (e.g. rancher-extension).

## Failed staging

| Symptom | Cause / fix |
|---|---|
| `no 'package.json' found in project path /workspace/source/app` (same for `go.mod` / `requirements.txt`) | Files nested one directory too deep. `--path` must be the project root. |
| `TurbopackInternalError: Symlink node_modules is invalid` | Next.js 16 Turbopack. Use `next build --webpack`. |
| Push gateway timeout / HTML error | Ingress cut a long build; staging often continues. Wait, then `epinio app show APP -o json`. |
| `show` 404 after push | App resource incomplete. `epinio app delete APP`, push again. |
| `stagingstatus: failed` (CLI JSON field) | `epinio app logs APP --staging`. |
| Pod never ready | `epinio app logs APP`. Usually bind address/`$PORT` or missing env (`epinio app env set`). |
| `authentication required` on `--git` push | No gitconfig selected. Create one, then push with `origin.git.gitconfig` in the manifest. |
