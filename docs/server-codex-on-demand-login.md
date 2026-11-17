# 给服务器 Codex：创建时失效，按需向 Windows 请求恢复登录

请直接修改、测试并部署当前 `poxiaosuitian-lang/icloud-hme` 项目。先核对 AGENTS.md、origin 和实际部署目录，保留已有修改、账号数据、任务、密钥及卷。用户明确要求**事件触发，不要新增每五分钟检查 Apple 会话的逻辑**。

## 已有客户端与目标

Windows 项目已经实现任务接收端：`internal/hmesync/recovery.go` 和 `internal/dashboard/sync_recovery.go`。用户勾选某账号某服务器的“允许服务器按需恢复登录”后，本机向服务器发起长轮询等待任务，关闭网页仍运行，本机程序退出后不在线。没有任务时客户端不访问 Apple、不读验证码、不重新登录。

遇到 `APPLE_ACCOUNT_LOGIN_REQUIRED`（产品称 `login_required`）才创建恢复任务。客户端收到任务后沿用现有 `apple-login` status/login/verify_device 流程，使用本机加密密码和 Mac 验证码；主密码不由任务接口读取/返回，不允许服务器直接调用 Windows localhost，也不要把本机端口暴露公网。

服务器已实现以下路由及队列；Web Cookie 恢复扩展见 `web-cookie-recovery.md`。保留已有 `verify_device` 和 `device_code_supported`。

## 精确协议

共用现有 `ICLOUD_HME_IMPORT_TOKEN` Bearer 鉴权，不复用服务 API token/管理员会话；恒定时间比较、严格 JSON、请求大小限制、禁止 query 凭据、压缩和未知字段。沿用集成接口脱敏日志，只记录固定操作及状态，不打印 body/lease/密钥。等待连接不要占用现有认证 `importSlots`，使用独立有界并发资源。认证通过的正常长轮询不是失败登录，不能累计成限流封禁。

### POST /api/integrations/appleservices/recovery/wait

```json
{"version":1,"client_id":"本机服务器配置的32位十六进制ID","sources":["appleservices-64位小写SHA256"]}
```

- sources 只允许已验证导入账号的 source_id，必须校验 verified_apple_id、verified_dsid、收件地址和身份绑定；禁止仅凭任意 source_id 创建账号或分配任务。
- 本机 sources 是明确授权的账号集合，任务必须属于集合内账号；空集合拒绝。建议上限 50。
- 等待最长 **25 秒**，无任务 HTTP 200：`{"success":true,"data":{"job":null}}`。这是等待服务器事件，不是定时检查 Apple。请求取消应立即清理等待者；普通 HTTP 代理超时应大于 35 秒。
- 有任务 HTTP 200：

```json
{"success":true,"data":{"job":{"id":"job_唯一随机ID","lease":"64位小写十六进制随机值","source_id":"appleservices-...","kind":"apple_login"}}}
```

- id 只能字母数字下划线短横线，最长128；kind 只能 `apple_login`。不包含 password、OTP、Cookie、任意URL/路径、代理、可执行命令或别名创建指令。
- 原子领取，租约至少 **240 秒**（本机单次恢复最大185秒）；绑定 client_id、source_id、已验证账号身份/导入版本、当前失效会话版本。一个账号同时最多一个有效恢复任务，多个窗口/机器/接口并发不能重复发起登录。
- 同一任务响应丢失或租约重发必须保留同一 job id；新租约可轮换。客户端持久化 job id 和结果，重复任务只确认已有结果，不重复调用登录。

### POST /api/integrations/appleservices/recovery/complete

```json
{"version":1,"client_id":"同上","job_id":"job_...","lease":"当前租约","outcome":"ready"}
```

outcome 仅允许 `ready`、`verification_required`、`failed`、`local_unavailable`。成功确认返回 `{"success":true}`。无正文秘密字段，所有错误静态错误码。校验当前租约、client_id、账号及身份版本；拒绝跨账号、过期和伪造租约。重复 complete 幂等，不重复触发创建。

- `ready`：不能单凭客户端字符串相信已恢复。核验当前账号 AppleSession 已由现有登录接口成功保存，并做实际只读 management 会话验证（可复用 ImportAppleLogin status）。会话缺失/验证失败不得将通道恢复。清除该账号对应会话版本的拒绝标记，让仍在运行的任务按**下一正常时隙**恢复新通道。停止的任务不重启，不补发错过时隙，不扩大创建频率/额度，不立刻重放失败的创建请求。
- `verification_required`：持久化暂停恢复，UI 明确“需在 Windows 面板完成验证”。同一账号不能靠新的 job id 绕过暂停，不自动请求短信。用户在面板完成手动登录后，可由成功保存的新会话版本解除服务器暂停；本机通过重新勾选或成功同步解除本机暂停。
- `failed`：保留原任务状态，不抹掉旧 Web/IMAP 凭据；失败重试有限并退避，例如最多3次，5/15/60分钟。重试需要显式队列状态机，不对每个失败创建都生成新任务。
- `local_unavailable`：电脑离线、账号离线、任务已撤销或本机忙。延迟同一任务并允许重新领取；不能立即生成新ID或循环发起Apple登录。本机在一次认证尝试后还有5分钟最短冷却，这只是防重，不是会话检查周期。
- 本机长期离线时显示“等待本机协助”，保留现有降级/暂停策略。不得无限占用创建请求或持有账号/调度全局锁等待网络。

## 事件入口与状态恢复

检查 `internal/server/creation_channels.go` 的 creationChannels/createOnChannel、`internal/server/dual_worker.go` 的 `APPLE_ACCOUNT_LOGIN_REQUIRED` 分支，以及批量/服务API创建入口。统一调用一个恢复请求方法：只有创建过程中确认新通道认证失效，且账号有合法导入绑定，才产生需求。预检查发现新通道无会话并准备降级时也要记录一次该需求，避免根本没发起创建就永远没有恢复请求。

`APPLE_ACCOUNT_ACTION_REQUIRED`、条款未确认、429限流、容量上限、一般网络错误、`ALIAS_CONFIRMATION_REQUIRED`、结果不明等不得被转换为自动重登录。别名创建可能已成功时必须保持原来的暂停与人工确认，不能重放生成/保留操作。

任务只保存必要状态于权限0600的服务器数据目录，原子写入并保留重启语义。账号删除、导入身份/代理/区域变化、密码/会话被人工更新时，失效旧任务/租约；恢复后用新会话版本更新双通道调度的 AppleRejected/AppleRevision 逻辑。已恢复事件重复到达不得清空别名记录或重建任务。严守已有锁顺序，不在网络请求期间持全局锁。

## 验收和部署

新增合成测试：无login_required不产生任务且不调用Apple；创建认证失败恰好一个任务；批量/多账号隔离；伪造/错误source拒绝；等待超时和取消；并发领取去重；租约和complete幂等；重启持久化；本机离线、需要OTP暂停；ready必须经真实只读核验；旧会话失败结果不能覆盖新登录；恢复不重复创建且不重启已停止任务；所有响应日志无密码/OTP/Cookie。不要用真实账号批量创建作为测试。

在 Linux 运行相应Go测试、前端检查和 git diff --check。仅提交代码、文档与测试，推送配置的私有remote并核对远端提交。按当前部署方式备份受限数据后重建程序或镜像，保留卷和 `.env`。不改现有同步密钥、账号、不关掉用户任务。

部署后核对 wait/complete 路由、服务版本；只读确认 Windows 已连接等待。没有真实自然失效事件时不要为了验收人为注销用户账号，也不要宣称已端到端恢复。报告部署提交、测试结果、等待连接是否可用、仍待何种事件验收。
