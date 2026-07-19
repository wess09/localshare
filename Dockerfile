ARG GO_IMAGE=docker.io/library/golang:1.25-bookworm
ARG NODE_IMAGE=docker.io/library/node:24.14.0-bookworm
ARG RUNTIME_IMAGE=docker.io/library/debian:bookworm-slim

FROM ${NODE_IMAGE} AS admin-builder

ARG PNPM_VERSION=11.2.2

WORKDIR /src/web/admin

RUN corepack enable && \
    corepack prepare "pnpm@${PNPM_VERSION}" --activate && \
    node --version && \
    pnpm --version

COPY web/admin/package.json web/admin/pnpm-lock.yaml web/admin/pnpm-workspace.yaml ./
RUN HUSKY=0 pnpm install --frozen-lockfile

COPY web/admin ./
RUN pnpm build

FROM ${GO_IMAGE} AS builder

ARG VCS_REF=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=admin-builder /src/web/admin/dist ./web/admin/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VCS_REF}" -o /out/localshare ./cmd/localshare

FROM ${RUNTIME_IMAGE}

ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
ARG SOURCE_REPOSITORY=https://github.com/wess09/localshare

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
