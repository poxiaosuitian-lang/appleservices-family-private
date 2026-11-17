# Web Cookie 按需恢复

适用于由 appleservices 导入、已验证身份并开启“允许服务器按需恢复登录”的账号。旧手工账号不参与。

## 运行方式

- 读取或创建隐藏邮箱时遇到 `UPSTREAM_UNAUTHORIZED` / `COOKIES_REQUIRED`，服务器记录 `web_session` 任务。一般网络失败、限流和创建结果不明不触发重登录。
- Windows 通过出站 HTTPS 长轮询等待任务，无需开放本地端口。不新增定时 Apple 会话探测。
- 本机读取对应账号的隐藏邮箱；需要时使用已有加密凭据和受信任设备验证登录，然后回传 Cookie。
- 服务器核验 Apple 身份、DSID、收件邮箱和隐藏邮箱访问后，以账号版本和租约双重校验保存 Cookie。旧请求不能覆盖新 Cookie。
- 不修改应用专用密码、账号配置、别名或任务计划。正在运行的任务在下一正常时隙恢复，停止的任务不重启，失败创建不重放。
- 本机退出或离线时等待；Apple 要求人工验证时暂停，在本机完成隐藏邮箱验证后重新启用按需恢复。

## 协议增量

沿用同步专用 Bearer token 和原有 `recovery/wait`、`recovery/complete`。

`wait` 增加可选 `kinds: ["apple_login", "web_session"]`。旧客户端省略此字段时仅领取 `apple_login`。同账号两个通道的任务、冷却和暂停分别记录。

`POST /api/integrations/appleservices/recovery/web-session` 严格接受以下八个字段：

`version, client_id, job_id, lease, source_id, apple_id, host, cookies`

必须有属于该客户端和已绑定账号的有效 Web 恢复租约。拒绝跨账号、过期或撤销的任务、主密码、应用专用密码及未知字段。正文上限 256 KiB，不跟随重定向，不记录凭据。

成功返回 `success: true`，`data` 含 `source_id`、`state: "ready"`、`web_verified: true`。该接口已原子完成恢复任务，之后的 `complete` 是幂等确认。

管理端 `GET /api/settings/recovery` 的账号项新增 `web_state` / `web_message`，原 `state` 仍表示 Apple Account 通道。此状态查询不访问 Apple。

## 部署与验收

先部署服务器，再替换 Windows dashboard 与账号工作进程。保留 `.env`、data 卷、现有 Compose 网络配置和本机加密配置。

测试覆盖任务去重、旧客户端兼容、双通道隔离、跨账号拒绝、撤销租约、重复提交、身份不符、并发配置变更以及旧请求覆盖防护。生产验收只读取路由、版本和等待连接；没有自然失效事件时，不人为注销账号或声称已实测完整恢复。
