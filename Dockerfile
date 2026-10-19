ARG NODE_IMAGE=node:24.18.0-bookworm-slim@sha256:6f7b03f7c2c8e2e784dcf9295400527b9b1270fd37b7e9a7285cf83b6951452d
ARG GO_IMAGE=golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd
ARG RUNTIME_IMAGE=alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web-build

WORKDIR /web
COPY web/app/package.json web/app/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/app ./
RUN mkdir -p public/vendor && cp node_modules/hls.js/dist/hls.light.min.js public/vendor/hls.light.min.js
RUN NODE_OPTIONS=--max-old-space-size=3072 npm run build

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS go-build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/jmlab ./cmd/jmlab

FROM ${RUNTIME_IMAGE}

RUN apk add --no-cache ca-certificates git openssh-client \
    && addgroup -S jmlab && adduser -S -G jmlab jmlab \
    && mkdir -p /app /config /data /cache /backup \
    && chown -R jmlab:jmlab /app /config /data /cache /backup

COPY --from=go-build /out/jmlab /app/jmlab
COPY --from=go-build /src/migrations /app/migrations
COPY --from=web-build /web/dist /app/web
ARG JMLAB_VERSION=dev
ARG JMLAB_REVISION=unknown
LABEL org.opencontainers.image.title="JMlab" \
      org.opencontainers.image.description="Modular virtual media server with Jellyfin compatible APIs" \
      org.opencontainers.image.version=$JMLAB_VERSION \
      org.opencontainers.image.revision=$JMLAB_REVISION \
      org.opencontainers.image.source="https://github.com/graysui/JMlab"
USER jmlab
WORKDIR /app
EXPOSE 8097
ENTRYPOINT ["/app/jmlab"]
