# syntax=docker/dockerfile:1
# Packages the release bundle's verified binaries; nothing is compiled here.
# Build context: a directory holding bin/linux-<arch>/soar-panel for each platform.
FROM --platform=$BUILDPLATFORM alpine:3.20 AS stage
ARG TARGETARCH
RUN apk add --no-cache libcap
COPY bin/linux-${TARGETARCH}/soar-panel /out/usr/local/bin/soar-panel
# The unprivileged user binds 53/80/443, like the native service's
# AmbientCapabilities=CAP_NET_BIND_SERVICE. Do not set no-new-privileges.
RUN chmod 0755 /out/usr/local/bin/soar-panel \
 && setcap cap_net_bind_service=+ep /out/usr/local/bin/soar-panel \
 && mkdir -p /out/var/lib/soar-panel \
 && chown 65532:65532 /out/var/lib/soar-panel \
 && chmod 0700 /out/var/lib/soar-panel

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/WhiteDNS/Soar-Server" \
      org.opencontainers.image.title="WhiteDNS Server Management" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=stage /out/ /
USER 65532:65532
VOLUME /var/lib/soar-panel
ENTRYPOINT ["/usr/local/bin/soar-panel"]
CMD ["run", "--container", "--db", "/var/lib/soar-panel/panel.db", "--dns-listen", "auto:53", "--listen", "127.0.0.1:2053"]
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
  CMD ["/usr/local/bin/soar-panel", "health", "--db", "/var/lib/soar-panel/panel.db"]
