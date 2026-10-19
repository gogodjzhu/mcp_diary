# 生产环境部署说明

本文档只描述：生产服务在哪里、怎么访问、怎么验证，以及怎么更新到新版本。

- 对外地址：https://mcp-diary.gogodjzhu.com/
- 部署方式：Docker Compose（服务器上的编排文件）
- 数据：Docker 具名卷 `gogodjzhu_mcp-diary-data`，挂载到容器 `/data`

---

## 1. 访问生产服务器

```bash
ssh -p 2212 -i /root/.ssh/ubuntu.gogodjzhu.pem ubuntu@gogodjzhu.com
```

编排文件所在目录：

```bash
cd /home/ubuntu/src/go/src/gogonotebook/cloud/docker-compose/bundle/gogodjzhu/
```

该目录下有：

| 文件 | 作用 |
|---|---|
| `docker-compose.yml` | 全站编排文件，`mcp-diary` 是其中一个服务 |
| `.env` | 提供 `MCP_PUBLIC_URL`、`GOOGLE_CLIENT_ID`、`GOOGLE_CLIENT_SECRET` 等变量 |

服务名（在 compose 中）：`mcp-diary`，容器名也是 `mcp-diary`。

---

## 2. 服务地址

| 用途 | 地址 |
|---|---|
| Web 界面 | https://mcp-diary.gogodjzhu.com/ |
| MCP 端点（Streamable HTTP） | https://mcp-diary.gogodjzhu.com/mcp |
| 健康检查 | https://mcp-diary.gogodjzhu.com/healthz |
| OAuth 授权服务器元数据 | https://mcp-diary.gogodjzhu.com/.well-known/oauth-authorization-server |
| 受保护资源元数据 | https://mcp-diary.gogodjzhu.com/.well-known/oauth-protected-resource |

容器只监听 `8080`，不直接发布到宿主机；对外由前置反向代理转发到 `mcp-diary:8080`。
因此**不要在服务器上用 `localhost:8080` 探测**，容器内的端口在宿主机上不通。

---

## 3. 查看当前状态

### 3.1 看运行的是哪个镜像

```bash
docker ps --filter name=mcp-diary --format "{{.Image}} {{.Status}}"
```

### 3.2 看服务自报版本

```bash
curl -s https://mcp-diary.gogodjzhu.com/healthz | python3 -m json.tool
```

关键字段：

| 字段 | 含义 |
|---|---|
| `status` | 正常时为 `ok` |
| `version` | 二进制版本，应与 Docker 镜像 tag 一致（如 `v0.3.0`） |
| `tools` | 当前注册的工具列表 |
| `auth_enabled` | 生产应为 `true` |
| `per_user` | 生产应为 `true`（每个用户独立工作区） |

### 3.3 看日志

```bash
docker logs --tail 100 mcp-diary
docker logs -f mcp-diary          # 实时跟随
```

启动日志里会打印 `workspace opened`（含 root、tools 列表）和 `HTTP server listening`，
可以据此确认容器确实起在该版本上。

---

## 4. 更新到新版本

前提：新版本的 Docker 镜像已经推送到 Docker Hub（如 `djzhu/mcp-diary:0.3.0`）。
发布镜像的流程不在本文档范围内。

### 步骤 1：确认镜像 tag 存在

```bash
docker pull djzhu/mcp-diary:0.3.0
```

如果报 `manifest unknown` / `not found`，说明该版本还没发布，请停止操作——
此时**不要**继续重启服务。

### 步骤 2：修改编排文件中的镜像版本

```bash
cd /home/ubuntu/src/go/src/gogonotebook/cloud/docker-compose/bundle/gogodjzhu/

# 修改前先备份
cp docker-compose.yml docker-compose.yml.bak.$(date +%Y%m%d)-before-0.3.0

# 把 mcp-diary 服务的 image 改成目标版本
vi docker-compose.yml
```

找到 `mcp-diary:` 这一节，把

```yaml
    image: djzhu/mcp-diary:0.2.1
```

改为

```yaml
    image: djzhu/mcp-diary:0.3.0
```

### 步骤 3：重建容器

```bash
docker compose pull mcp-diary
docker compose up -d mcp-diary
```

`up -d` 只重建 `mcp-diary` 这一个容器，不影响同一编排文件里的其他服务。
数据保存在具名卷里，重建容器不会丢失日记数据。

### 步骤 4：验证

```bash
docker ps --filter name=mcp-diary --format "{{.Image}} {{.Status}}"
curl -s https://mcp-diary.gogodjzhu.com/healthz | python3 -m json.tool
```

确认：

- 镜像 tag 是目标版本；
- `status` 为 `ok`；
- `version` 与目标版本一致。

再用浏览器打开 https://mcp-diary.gogodjzhu.com/ 确认 Web 界面可正常登录使用。

---

## 5. 回滚

```bash
cd /home/ubuntu/src/go/src/gogonotebook/cloud/docker-compose/bundle/gogodjzhu/

# 把 image 改回上一个稳定版本，例如
#   image: djzhu/mcp-diary:0.2.1
vi docker-compose.yml

docker compose up -d mcp-diary
```

数据卷保持不变，回滚只切换程序版本。

---

## 6. 关键约束与注意事项

### 6.1 必须使用已发布的镜像 tag

编排文件里写了 `pull_policy: always`。当指定的 tag 在 Docker Hub 上不存在时，
`docker compose up -d` 会退回使用本地已有的旧镜像，**容器照常启动，但运行的是旧版本**。

判断方法：`docker ps` 显示的镜像 tag 与 `healthz` 的 `version` 若不是你想要的版本，
就是这种情况。务必先执行第 4 节步骤 1 确认镜像可拉取。

### 6.2 数据位置

```bash
docker inspect mcp-diary --format '{{json .Mounts}}'
```

- 卷：`gogodjzhu_mcp-diary-data`
- 容器内路径：`/data`
- 结构：
  - `/data/auth/` — OAuth 状态（客户端注册、会话等）
  - `/data/users/` — 每个用户一个工作区（按邮箱安全化命名）

备份时直接备份该卷即可；升级/回滚不需要动卷。

### 6.3 环境变量

服务依赖 `.env` 中的：

- `MCP_PUBLIC_URL` — 必须等于对外域名 `https://mcp-diary.gogodjzhu.com`，否则 OAuth issuer 不匹配；
- `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` — Google OAuth 凭据，改动后需重建容器。

修改 `.env` 后要执行 `docker compose up -d mcp-diary` 才会生效。

### 6.4 网络与代理

该服务在编排文件中接入了内部代理容器（`trojan-client`），用于访问外网。
更新时不要改动 `networks` 配置，否则服务可能无法完成 OAuth 回调。

---

## 7. 常见故障速查

| 现象 | 排查 |
|---|---|
| 改了版本但 `healthz` 仍是旧版本 | 目标 tag 未发布，`up -d` 用了本地旧镜像；执行 `docker compose pull mcp-diary` 看是否报 `not found` |
| `healthz` 打不开或 502 | `docker ps` 看容器是否在运行；`docker logs mcp-diary` 看启动报错；确认反向代理指向 `mcp-diary:8080` |
| 登录后反复跳回登录页 | `.env` 的 `MCP_PUBLIC_URL` 与访问域名不一致，或 `AUTH_ENCRYPTION_KEY` 与已有数据不匹配 |
| 所有用户都需要重新授权 | `AUTH_ENCRYPTION_KEY` 被更换，导致旧的 OAuth 记录无法解密；恢复原密钥即可 |
| 容器不断重启 | `docker logs mcp-diary` 查看原因；常见为卷权限或 `--root` 路径错误 |
