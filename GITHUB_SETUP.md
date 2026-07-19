# GitHub 与阿里云构建说明

GitHub 只作为源码仓库使用。不要配置 Docker Hub token，也不要通过 GitHub Actions 推送 Docker Hub 镜像。

本地远程仓库应指向 GitHub:

```bash
git remote set-url origin https://github.com/wess09/localshare.git
git push origin master
```

镜像构建由阿里云容器镜像服务完成，目标镜像为:

```bash
crpi-gukwnnx8iuh9qpez-vpc.cn-shanghai.personal.cr.aliyuncs.com/hajiming/localshare:latest
```

阿里云构建配置应监听 GitHub 仓库 `master` 分支，并使用仓库根目录的 `Dockerfile`。Dockerfile 内部会先执行 `web/admin` 的 `pnpm build`，再执行 Go 编译，因为 Go binary 需要嵌入 `web/admin/dist/*`。
