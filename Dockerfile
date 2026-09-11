# Multi-stage build. The builder stage always runs on the build host's
# native architecture (BUILDPLATFORM) and cross-compiles for the target
# architecture (TARGETARCH) via Go's own toolchain, so multi-arch builds
# never fall back to a QEMU emulator.
FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS builder

ARG TARGETOS=linux
ARG TARGETARCH
ENV CGO_ENABLED=0

WORKDIR /src
COPY app/go.mod ./
RUN go mod download
COPY app/ ./
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/server .

# Final stage ships only the static binary and CA certificates: no shell,
# no package manager, no build tools.
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/server /server

# Non-root by default; the Deployment's securityContext restates this so
# it does not depend on the image's default alone.
USER 65532:65532

# Single HTTP port. The real port number comes from the PORT env var at
# runtime; this EXPOSE is documentation only.
EXPOSE 8080

ENTRYPOINT ["/server"]
