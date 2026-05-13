# standard-elevated AppChart

An Epinio AppChart that extends the standard chart with elevated RBAC — specifically, read access to `apps.application.epinio.io` CRDs and pull access to the internal registry. The Epinio MCP server uses this chart so it can read `spec.blobuid` from app CRDs to retrieve source tarballs from S3.

## Why this isn't in the Epinio Helm charts repo

This chart grants cluster-level RBAC to every app deployed with it. That's intentional for the MCP server (it needs to read app CRDs across namespaces), but it's not appropriate as a default catalog option — operators shouldn't be able to one-click grant elevated privileges to arbitrary apps. Keeping it here requires an explicit `kubectl apply` opt-in by a cluster admin.

## What it adds over the standard chart

- **Per-app ServiceAccount** with `registry-creds` imagePullSecret
- **ClusterRole** granting read access to `apps.application.epinio.io` CRDs
- **ClusterRoleBinding** tying the ServiceAccount to the role

## The chart tarball is already embedded

For normal installs you don't need to package this chart. The compiled tarball is already baked into `manifests/chart-server.yaml` as base64 ConfigMap data. Just apply that manifest — see `INSTALL.md` for the full flow.

## Rebuilding the tarball (if you modify the chart)

```bash
# From the repo root
helm package appcharts/standard-elevated -d /tmp/
```

Then base64-encode the output and update the `binaryData.chart.tgz` field in `manifests/chart-server.yaml`:

```bash
base64 -w0 /tmp/epinio-application-0.1.26.tgz
```

## Registering the AppChart

The AppChart registration manifest is at `manifests/standard-elevated-appchart.yaml`. Apply it after the chart server is running:

```bash
kubectl apply -f manifests/standard-elevated-appchart.yaml
```

## Using it in an app

In `epinio.yml`:

```yaml
configuration:
  appchart: standard-elevated
```

Or via CLI:

```bash
epinio push --name my-app --app-chart standard-elevated
```
