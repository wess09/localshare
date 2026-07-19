FROM golang:1.25-bookworm AS builder

ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
ARG SOURCE_REPOSITORY=https://github.com/wess09/localshare
ARG NODE_VERSION=24.14.0
ARG PNPM_VERSION=11.2.2
ARG TARGETARCH

WORKDIR /src

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    xz-utils && \
    rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
      amd64) node_arch="x64" ;; \
      arm64) node_arch="arm64" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" \
      | tar -xJ -C /usr/local --strip-components=1; \
    corepack enable; \
    corepack prepare "pnpm@${PNPM_VERSION}" --activate; \
    node --version; \
    pnpm --version

COPY go.mod go.sum ./
RUN go mod download

COPY web/admin/package.json web/admin/pnpm-lock.yaml web/admin/pnpm-workspace.yaml ./web/admin/
RUN cd web/admin && HUSKY=0 pnpm install --frozen-lockfile

COPY . .
RUN cd web/admin && pnpm build
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VCS_REF}" -o /out/localshare ./cmd/localshare

FROM debian:bookworm-slim

LABEL org.opencontainers.image.source="${SOURCE_REPOSITORY}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}"

WORKDIR /localshare

COPY --from=builder /out/localshare /localshare/localshare
COPY docker /localshare/docker
COPY static /localshare/static
COPY pywebio_static /localshare/pywebio_static

RUN printf '%s\n' "${VCS_REF}" > /localshare/BUILD_REVISION && \
    printf '%s\n' "${BUILD_DATE}" > /localshare/BUILD_DATE

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    nginx \
    ca-certificates && \
    rm -f /etc/nginx/sites-enabled/default && \
    rm -rf /var/lib/apt/lists/* && \
    ln -sf /dev/stdout /var/log/nginx/access.log && \
    ln -sf /dev/stderr /var/log/nginx/error.log

EXPOSE 1022
EXPOSE 80
EXPOSE 443

RUN ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo 'Asia/Shanghai' >/etc/timezone

CMD ["/localshare/docker/start.sh"]
