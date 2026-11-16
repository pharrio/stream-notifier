FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY locales ./locales
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /notifier . \
    && mkdir -p /runtime/etc/ssl/certs \
    && cp /etc/ssl/certs/ca-certificates.crt /runtime/etc/ssl/certs/ \
    && chmod 755 /runtime /runtime/etc /runtime/etc/ssl /runtime/etc/ssl/certs \
    && chmod 644 /runtime/etc/ssl/certs/ca-certificates.crt

FROM scratch
LABEL org.opencontainers.image.licenses="MIT"
COPY --from=build /runtime/ /
COPY --from=build /notifier /notifier
COPY third_party /licenses
COPY LICENSE /licenses/STREAM-NOTIFIER-LICENSE
USER 65532:65532
ENTRYPOINT ["/notifier"]
