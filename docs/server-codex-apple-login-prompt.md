# 服务器 Apple 新通道自动登录部署

将以下内容交给服务器 Codex：

> 请在当前实际部署的 `poxiaosuitian-lang/icloud-hme` checkout 完成更新和部署。Windows 端已经实现同步后的 Apple 新通道登录及本机验证码协助，服务器配套实现已推送该仓库，提交 `939e709`（或包含该提交的后续版本）。先读 AGENTS.md，核对 origin 和当前部署方式；保留现有账号、数据卷、任务、环境变量和专用同步密钥。核对并合并远端 main，保留本机未提交修改，不要 reset 或删除 data。
>
> 阅读仓库根目录 `WINDOWS_LOGIN_INTEGRATION.md`。必须部署包含 `POST /api/integrations/appleservices/apple-login` 的版本，不能只重启旧镜像。该接口继续使用已有 `ICLOUD_HME_IMPORT_TOKEN`，不需要新密钥或管理员密码，不修改公网端口/代理配置。主 Apple 密码和 OTP 仅用于本次认证，不落盘、不打日志；保存的是 Apple Account 独立会话。
>
> 在 Linux 运行 `go test ./internal/account ./internal/server ./internal/icloudmail/apple`、相关构建和 `git diff --check`。Windows 侧已通过新增认证测试；现有 POSIX 文件权限用例必须在 Linux 上验收。按现有方式备份受限数据目录后重建并部署；若当前使用本仓库 Compose build，可执行 `docker compose up -d --build`，务必保留原卷及 `.env`。
>
> 检查新路由：不带 Bearer 返回 401，带无效请求只做参数校验，不进行真实 Apple 登录。然后通知我 Windows 面板可以点击“同步到服务器 → 确认同步”。客户端会先导入 Web+IMAP，再 status 检查新通道，必要时登录并最多自动尝试一次本机验证码；需手动验证码时在 Windows 面板输入，不要发到聊天。验收以新接口返回当前 source_id 的 state=ready、服务器账号 has_apple_session=true 为准，不创建别名、不兑换或购买、不触发批量任务。
>
> 返回实际部署提交、测试结果、新接口状态及真实认证是否完成；未联调则明确写待 Windows 端验收，不要将旧通道成功当作新通道成功。部署问题和上游认证错误只返回脱敏摘要，不要输出密码、验证码、Cookie、API Key 或完整 Apple 响应。

## 本地流程

- 确认同步后先执行旧导入；只有成功才继续登录新通道。
- 新通道已有会话时，服务器实际读取管理会话资源验证并续期，客户端不发送主密码或验证码。
- 需要登录时，本机向用户选定的 HTTPS 服务器发送保存的主密码；服务器沿用现有 SRP/Apple Account 登录实现。
- 服务端 challenge 与已验证导入账号、待验证会话及有效期绑定；浏览器手动登录和集成登录不能混用 challenge。
- 本机验证码只尝试一次，短信验证不使用本机验证码。未完成时保留旧通道成功状态并提示手动验证，禁止自动循环提交。
- 新接口缺失时明确显示服务器需要更新，不宣称全部完成。
