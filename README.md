# Sub2API（个人分支）

本仓库是 [Sub2API](https://github.com/kiss-kedaya/sub2api) 的**私有二次开发分支**，不是官方仓库，不对外分发。

上游是 AI API 网关，把订阅制账号的配额按统一的 OpenAI / Anthropic / Gemini 协议暴露给下游客户端，并负责账号调度、额度计费与用量统计。本分支在上游基础上合并生产补丁，修复实际运行中暴露的缺陷。

当前版本：**2.2.19**（见 `backend/cmd/server/VERSION`）

## 与上游的关系

- 基线来源：上游 v2.2.16 + 本分支的生产补丁
- 历史经过压缩重排；上游的完整合并历史保留在本地 tag `backup-full-history-20261023`，不在本仓库分支中
- 本仓库**不跟随上游自动更新**，需要摘取上游改动时按需手动 cherry-pick

## 本分支相对上游的主要修复

| 修复 | 说明 |
| --- | --- |
| Grok 视频结算 | 兼容站返回 `status:"completed"` 而非官方 `done`，导致计费永不命中、任务无限轮询。状态解析统一为单一归一化函数，计费与轮询共用 |
| 媒体任务上限 | 视频计费任务增加 6 小时 TTL 与次数上限；超龄先补一次结算，仍未结算则终止并释放预留额度 |
| 流式帧聚合 | 多行 SSE 聚合护栏遇到超长非 SSE 响应时降级为原字节透传，不再把正常响应判成流错误 |
| 会话身份推导 | 顶层 `input_text` 作为用户锚点、助手 `output_text` 排除出锚点、前缀槽位仅在无历史轮次时写入，避免每轮换账号 |
| 邮件出站 | SMTP 私网放行改为跟随 `security.url_allowlist.allow_private_hosts`，不再写死拒绝内网邮件主机 |
| 定时质检日志 | 未配置渲染器时只提示一次并退回本地评估，不再每次计划执行都记 ERROR |

## 技术栈

- 后端：Go 1.27、Gin 1.9、ent 0.14、PostgreSQL 15+
- 前端：Vue 3.5、TypeScript 5.6、Vite 5
- 缓存：Redis 7+ / Valkey，亦可使用内存实现

## 本地开发

```bash
# 后端（需要 Go 1.27+）
cd backend
go build ./...

# 单元测试（部分用例带 unit 构建标签）
go test -tags unit ./internal/service/

# 前端
cd frontend
pnpm install
pnpm run build   # 产物写入 ../backend/internal/web/dist/
```

前端构建完成后，后端以 `-tags embed` 编译即可内嵌静态资源。

## 部署

部署脚本与编排位于 `deploy/`，生产使用容器方式：

```bash
cd deploy
cp .env.example .env      # 必须设置 POSTGRES_PASSWORD
docker compose up -d
```

关键环境变量（`.env`）：

| 变量 | 说明 |
| --- | --- |
| `POSTGRES_PASSWORD` | 必填，数据库密码 |
| `JWT_SECRET` | 建议固定值，否则重启后登录会话失效；`openssl rand -hex 32` |
| `TOTP_ENCRYPTION_KEY` | 建议固定值，否则重启后 2FA 配置无法解密 |
| `SECURITY_URL_ALLOWLIST_ALLOW_PRIVATE_HOSTS` | 是否允许上游 / 邮件目标落在私网 |

服务默认监听 8080，首次启动通过 `/setup` 完成初始化。

## 数据与备份

- 数据库迁移以增量方式执行，位于 `backend/migrations/`
- 升级前请备份数据库与 `docker-compose.yml`
- 部署脚本在构建前会做数据库导出、镜像固定与源码快照

## 许可

上游采用 LGPL-3.0，本分支沿用同一许可，仅用于个人部署。

## 上游与致谢

- 上游项目：<https://github.com/kiss-kedaya/sub2api>
- 原始项目：<https://github.com/Wei-Shaw/sub2api>
