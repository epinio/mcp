# Multi-stage build for epinio-mcp.
#
# Build: docker build -f install/Dockerfile -t ghcr.io/krumware/epinio-mcp:<tag> .
# Run:   docker run -p 8080:8080 -e EPINIO_API_URL=... ghcr.io/krumware/epinio-mcp:<tag>
#
# The final image is distroless (nonroot), ~20MB. No shell, no package manager.
# All runtime config comes from env vars — see install/epinio-mcp.yaml for the
# full set of variables the MCP reads.

FROM golang:1.25-alpine AS build

WORKDIR /src

# Layer cache for deps — copy go.mod/go.sum first so module downloads don't
# invalidate on every source edit.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static build. CGO disabled so the binary runs on distroless without glibc.
# -trimpath + -ldflags "-s -w" strip paths + symbols for a smaller image.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/epinio-mcp \
    .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/epinio-mcp /epinio-mcp

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/epinio-mcp"]
