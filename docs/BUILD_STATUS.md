# 查看 GitHub Actions 构建状态

## 🔍 检查构建进度

访问以下链接查看你的 GitHub Actions 构建状态：

**https://github.com/hajiming/localshare/actions**

## 📊 构建状态说明

- 🟡 **黄色圆圈** - 正在构建中
- ✅ **绿色对勾** - 构建成功
- ❌ **红色叉号** - 构建失败

## ⏱️ 预计时间

Docker 镜像构建大约需要 **2-5 分钟**

## 🎯 构建成功后

一旦看到绿色对勾，你的镜像就已经推送到 Docker Hub 了：

**镜像地址**: `hajiming/localshare:latest`

## 🚀 部署命令

构建成功后，在你的服务器上运行：

```bash
# 创建配置目录
mkdir -p ~/localshare_config

# 拉取镜像
docker pull hajiming/localshare:latest

# 确认拉到的镜像版本，应该等于 GitHub Actions 里的 commit SHA
docker image inspect hajiming/localshare:latest \
  --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}'

# 旧容器不会因为 docker pull 自动更新，必须删除并重新创建
docker rm -f localshare || true

# 运行容器
docker run -d \
  --name localshare \
  --restart=always \
  -v ~/localshare_config:/config \
  -e APP_SERVER_NAME=remote.nanoda.work \
  -p 1022:1022 \
  -p 80:80 \
  -p 443:443 \
  hajiming/localshare:latest

# 查看日志
docker logs -f localshare

# 也可以从容器内确认实际代码版本
docker exec localshare cat /localshare/BUILD_REVISION
```

如果不想受 `latest` 本地缓存影响，可以直接部署本次 Actions 输出的 SHA tag：

```bash
docker pull hajiming/localshare:sha-提交前12位
docker rm -f localshare || true
docker run -d \
  --name localshare \
  --restart=always \
  -v ~/localshare_config:/config \
  -e APP_SERVER_NAME=remote.nanoda.work \
  -p 1022:1022 \
  -p 80:80 \
  -p 443:443 \
  hajiming/localshare:sha-提交前12位
```

## 🔧 DNS 配置

确保已添加 DNS 记录：

```
类型: A
主机记录: remote
记录值: 你的服务器IP
```

## ✅ 测试连接

部署后测试：

```bash
ssh -R /:localhost:8080 -p 1022 remote.nanoda.work
```

应该返回类似：

```
The public entrypoint for your local web service is:
http://remote.nanoda.work/abc123xyz456
```
