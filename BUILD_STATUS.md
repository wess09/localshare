# 阿里云镜像构建状态

本项目不使用 GitHub Actions 推送 Docker Hub 镜像。代码推送到 GitHub 后，由阿里云容器镜像服务根据仓库分支自动构建。

镜像地址:

```bash
crpi-gukwnnx8iuh9qpez-vpc.cn-shanghai.personal.cr.aliyuncs.com/hajiming/localshare:latest
```

Dockerfile 默认只引用阿里云镜像源作为基础镜像:

```bash
registry.cn-hangzhou.aliyuncs.com/library/golang:1.25-bookworm
registry.cn-hangzhou.aliyuncs.com/library/node:24.14.0-bookworm
registry.cn-hangzhou.aliyuncs.com/library/debian:bookworm-slim
```

部署时使用现有 PostgreSQL，必须显式提供 `DATABASE_URL`:

```bash
docker pull crpi-gukwnnx8iuh9qpez-vpc.cn-shanghai.personal.cr.aliyuncs.com/hajiming/localshare:latest

docker image inspect \
  crpi-gukwnnx8iuh9qpez-vpc.cn-shanghai.personal.cr.aliyuncs.com/hajiming/localshare:latest \
  --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}'

docker rm -f localshare || true

docker run -d \
  --name localshare \
  --restart=always \
  -v ~/localshare_config:/config \
  -e APP_SERVER_NAME=remote.nanoda.work \
  -e DATABASE_URL='postgres://localshare:localshare@postgres:5432/localshare?sslmode=disable' \
  -p 1022:1022 \
  -p 80:80 \
  -p 443:443 \
  crpi-gukwnnx8iuh9qpez-vpc.cn-shanghai.personal.cr.aliyuncs.com/hajiming/localshare:latest
```

验证:

```bash
docker logs -f localshare
docker exec localshare cat /localshare/BUILD_REVISION
ssh -R /:localhost:8080 -p 1022 remote.nanoda.work -- --output json
```
