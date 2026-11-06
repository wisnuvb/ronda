# syntax=docker/dockerfile:1

# Build on the machine running the build and cross-compile for the target, so a multi-arch image needs no emulation.
# GO_VERSION is the toolchain pinned in go.mod (CI passes it); GOTOOLCHAIN=auto fetches the right one if this lags.
ARG GO_VERSION=1.27.3
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build
ENV GOTOOLCHAIN=auto CGO_ENABLED=0
WORKDIR /src
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ronda ./cmd/ronda
# The state directory, owned by the unprivileged user the image runs as (a named volume inherits this).
RUN mkdir -p /rootfs/var/lib/ronda /rootfs/etc/ronda && chown -R 65532:65532 /rootfs/var/lib/ronda

# Nothing but the binary, the CA certificates (HTTPS checks need them) and the state directory. The time zone
# database is inside the binary.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ronda /usr/local/bin/ronda
COPY --from=build --chown=65532:65532 /rootfs/var/lib/ronda /var/lib/ronda
COPY --from=build /rootfs/etc/ronda /etc/ronda

# Listen on every interface of the container; publish the port to loopback on the host (see deploy/docker-compose.yml).
ENV RONDA_LISTEN=0.0.0.0:9191 \
    GOMEMLIMIT=192MiB
USER 65532:65532
EXPOSE 9191
VOLUME /var/lib/ronda
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/ronda", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/ronda"]
CMD ["run", "-config", "/etc/ronda/config.json"]
