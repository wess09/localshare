# GitHub 仓库设置指南

## 问题原因

你推送到了原作者的仓库 `wess09/localshare`，GitHub Actions 只会在**你自己的仓库**中运行。

## 解决步骤

### 1. 在 GitHub 上创建你自己的仓库

访问: https://github.com/new

- Repository name: `localshare`
- 选择 Public 或 Private
- **不要**勾选 "Initialize this repository with a README"
- 点击 **Create repository**

### 2. 修改本地仓库的远程地址

```bash
cd c:\Users\Azur\Desktop\项目\localshare
git remote set-url origin https://github.com/hajiming/localshare.git
git push -u origin master
```

### 3. 添加 Docker Hub Token

1. 访问: https://github.com/hajiming/localshare/settings/secrets/actions
2. 点击 **New repository secret**
3. Name: `DOCKERHUB_TOKEN`
4. Value: (你的 Docker Hub Access Token)
5. 点击 **Add secret**

### 4. 触发构建

```bash
# 做一个小修改触发 Actions
git commit --allow-empty -m "Trigger GitHub Actions"
git push
```

### 5. 查看构建状态

访问: https://github.com/hajiming/localshare/actions

---

## 创建 Docker Hub Access Token

如果还没有创建 token:

1. 访问: https://hub.docker.com/settings/security
2. 点击 **New Access Token**
3. Description: `GitHub Actions`
4. Access permissions: `Read, Write, Delete`
5. 点击 **Generate**
6. **立即复制** token (只显示一次)
