# 给服务器 Codex 的任务提示词

请直接完成下面的开发、测试和部署，保留现有账号和运行数据。先读仓库 AGENTS.md。目标仓库是 `https://github.com/poxiaosuitian-lang/icloud-hme`，在服务器当前部署的 checkout 中工作，并核对 origin；有 GitHub 连接时使用它核对仓库。不要要求用户把 Apple 密码、Cookie 或 Token 发到聊天。

本地 Windows 项目 `poxiaosuitian-lang/appleservices-family-private` 已实现同步客户端，需要你给服务器实现下述**精确协议**。本地项目的协议文档在 `docs/server-codex-hme-import-prompt.md`，客户端在 `internal/hmesync/client.go`。不要改成不同的路由或字段。

## 接口

`POST /api/integrations/appleservices/accounts`

鉴权：`Authorization: Bearer <专用同步密钥>`。新增环境变量 `ICLOUD_HME_IMPORT_TOKEN`，至少 32 个随机字符，不允许空白；未配置时关闭此接口。不要复用或扩大现有 `ICLOUD_HME_API_TOKEN` 权限，不要改变管理员会话或 CSRF 规则。此接口仅凭专用 Bearer 认证，不使用浏览器 Cookie，所以无需 CSRF。恒定时间比较，限制请求大小（建议 256 KiB）及失败频率。通过 HTTPS 反向代理提供；本地客户端禁止 HTTP、URL 内凭据、跳转以及自动重试。

请求 JSON（示例全部是假值）：

```json
{
  "version": 1,
  "source_id": "appleservices-<64位小写SHA256十六进制>",
  "apple_id": "owner@example.test",
  "name": "账号名称",
  "icloud_email": "owner@icloud.com",
  "host": "icloud.com",
  "cookies": {
    "X-APPLE-WEBAUTH-TOKEN": "synthetic-token",
    "X-APPLE-WEBAUTH-USER": "synthetic-user"
  },
  "app_password": "abcd-efgh-ijkl-mnop"
}
```

`source_id` 的计算规则：字符串 `appleservices-hme-v1` + 一个 NUL 字节 + `strings.ToLower(strings.TrimSpace(apple_id))`，取 SHA256 小写十六进制，前缀 `appleservices-`。服务端重算并校验。

`host` 只允许 `icloud.com` / `icloud.com.cn`。`cookies` 是名称到值的对象；不是浏览器完整 Cookie 文件。密码是应用专用密码，不是 Apple 主密码。不得接受请求指定的代理、IMAP host、任意服务 URL 或其他本地文件路径。使用既有固定 iCloud IMAP 配置，避免 SSRF。

## 核验与幂等保存

1. 在锁外创建隔离候选账号，沿用现有客户端做真实 Apple validate，核对返回的账号身份与 `apple_id` 一致。不能只凭请求字段判断身份。
2. 实际读取 HME 别名列表，不能把 validate 成功或缓存计数当成 HME 验收。核对 `icloud_email` 属于该账号的已验证转发候选且为主 iCloud 收件地址，不能误用隐藏邮箱别名；不修改 Apple 转发配置。
3. 用 `icloud_email` + `app_password` 做 IMAP 登录验证，只验证连接/认证，不发邮件、不改邮件、不创建别名、不启动自动注册任务。
4. 全部成功再保存。按 `source_id` 持久化源账号映射并 upsert；相同源账号重复请求、响应丢失后的手动重试、容器重启后的同步，必须返回同一个服务器账号 ID。请求并发也不能产生重复账号。
5. 已有旧账号没有 source_id 时，可以凭服务端已验证的相同 Apple 身份及匹配收件地址认领并附加 source_id；出现多条匹配或身份冲突返回 409，不覆盖。保留已有 aliases、使用记录、任务、Apple Account 第二通道会话及用户配置。
6. 不要直接串用当前“添加账号”接口后再调用“设置密码”：现有新增失败也可能保存 status=error，会造成半成品。请添加事务式 manager 方法：验证完整候选后，在提交前再次检查当前账号身份和版本，原子保存凭据、身份映射；失败回滚，保留原账号凭据。遵守现有锁顺序，避免长时间网络请求持全局锁。
7. Cookie/应用专用密码只保存于服务器受限数据目录，文件权限 0600（沿用现有账号持久化并加强权限）。不写日志、Git、错误正文、审计明文或 API 响应。不要把客户端返回的 raw Apple 响应公开。

成功仅在核验和持久化全部完成后返回 HTTP 200（更新）或 201（新增）：

```json
{
  "success": true,
  "data": {
    "id": "acc_01234567",
    "source_id": "appleservices-<请求中的相同值>",
    "status": "active",
    "hme_verified": true,
    "imap_verified": true
  }
}
```

`id` 仅字母数字下划线短横线，最长 128。其他字段可以增加不敏感摘要，但不得返回秘密。客户端要求以上字段全部匹配才报告成功。失败返回 `success:false` 和静态错误码；401/403 密钥错误，409 身份冲突，422 Apple 会话/HME/IMAP 核验失败，503 上游暂时不可用。任何失败都不得覆盖旧凭据；不要自动重试账号写入或 Apple 创建动作。

## 修改位置与部署

参考现有 `internal/server/account_handlers.go`、`internal/server/auth.go`、`internal/server/server.go`、`internal/account/manager.go`、`internal/account/public.go`、`internal/mail`。新增专用 handler 和 manager 事务方法、映射字段与迁移逻辑，保持旧 accounts.json 可读取。修改 `main.go` 环境变量装配、Docker Compose 环境变量透传、API.md 与 README，实际路径以当前版本为准。

该导入接口只接入 Web HME + IMAP，不把 Cookie 当作 `apple_account_session`，不混入本地 Mac/GSA/PET 状态，不生成额外应用专用密码。同步后的 Apple Account 新通道认证由独立接口完成，见 [新通道部署提示词](server-codex-apple-login-prompt.md)；不要通过修改本导入协议来模拟认证。

新增测试：鉴权失败不访问 Apple、不返回秘密；合法新增/更新；无效版本/host/source_id；账号错配；HME 失败；IMAP 失败；保存失败回滚；相同源并发与重启去重；旧账号认领冲突；已有 AppleSession/任务保留；所有响应和日志无凭据。测试用本地合成响应，不消耗真实账号。

运行 Go 测试、相关前端检查及 git diff --check。仅提交代码、文档、测试，推送到已配置的目标 GitHub 仓库并核对远端提交。

部署前确认现有部署方式，受限备份 data 后替换程序/镜像，保留卷与账号。若使用本仓库 compose 构建，配置专用导入密钥，再 `docker compose up -d --build`。不能使用 README 中其他作者的镜像来代替本仓库代码。不要删除 data 或重置账号。

在服务器本地生成专用随机密钥并保存到权限 0600 的环境文件，Compose 透传 `ICLOUD_HME_IMPORT_TOKEN`；确保环境文件不被 Git 跟踪。不要在聊天或日志打印密钥。告知用户从哪个服务器文件安全取出密钥，填入 Windows 面板的“同步到服务器”窗口。

最后回复：部署版本、可填写到 Windows 面板的 HTTPS 根地址、密钥保存位置、测试结果、是否已完成真实联调。没有真实同步就明确写“待 Windows 面板首次同步验收”，不要声称已经导入账号。
