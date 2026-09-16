FROM golang:1.25-alpine AS build

WORKDIR /src

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY proto ./proto

ENV CGO_ENABLED=0

ARG VERSION=dev
ARG REVISION=unknown
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" -o /out/waf-geo ./cmd/geo

FROM alpine:3.22

ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="placitum/geo" \
      org.opencontainers.image.description="Placitum geo coder: country and ASN by address over gRPC and HTTP" \
      org.opencontainers.image.source="https://github.com/exemt/placitum-geo" \
      org.opencontainers.image.licenses="LicenseRef-Placitum" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

WORKDIR /app

COPY --from=build /out/waf-geo /usr/local/bin/waf-geo

RUN adduser -D -H -u 10004 wafgeo

RUN mkdir -p /var/lib/waf/geo && chown wafgeo /var/lib/waf/geo

USER wafgeo

ENV WAF_GEO_COUNTRY=/app/data/country \
    WAF_GEO_ASN=/app/data/asn \
    WAF_GEO_FETCH_DIR=/var/lib/waf/geo \
    WAF_GEO_CONTROLLER_URL=http://controller:8080 \
    WAF_GEO_HTTP=:8092 \
    WAF_GEO_GRPC=:50051 \
    WAF_NATS_URL=nats://nats:4222

EXPOSE 8092 50051

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8092/healthz || exit 1

CMD ["waf-geo"]
