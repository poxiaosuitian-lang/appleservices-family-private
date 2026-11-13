# 新账号自动创建 iCloud 主邮箱

面板新增账号（包括空列表中的首个账号）时，在该账号独立状态目录建立 `primary-mail-status.json` 任务。成功登录后后台自动检查 iCloud Mail：已有邮箱直接复用；没有邮箱才检查候选名称并提交创建。旧账号升级、刷新页面不会自动创建邮箱；可手动点击“检查并创建邮箱”。命令行直接启动的未登记账号不会自动获得创建任务。

这是主 `@icloud.com` 邮箱，不是“隐藏邮件地址”，不要求加入家庭组或购买 iCloud+。

默认名称取 Apple 账号前缀的小写英文字母和数字，必要时补齐合法前缀/长度；重名时依次检查附带账号哈希短后缀的候选名，最多四个。实际可用性以 Apple 回应为准。

## 协议依据

Apple 的[创建主要邮件地址说明](https://support.apple.com/zh-cn/guide/icloud/mmdd8d1c5c/icloud)提供网页创建入口。2026-11-11 检查的公开 [Mail 网页脚本](https://www.icloud.com/applications/mail2/2640Hotfix103/zh-cn/main.js)使用 setup 返回的 `mccgateway` 服务：

- `POST mailacct/v1/web/emailAvailability`：`email`，返回同一名称和 `available`。
- `POST mailacct/v1/web/activateEmail`：`email`，返回 `email`。网页的 `patternId` 和 `entryPoint` 为选用建议名/页面来源元数据，本实现使用自选名称，不伪造建议模式。
- setup `POST /setup/ws/1/validate`：检查当前账号与 `webservices.mail`。官方 Mail 同样以 mail 服务是否存在判断是否已经开通。

网页 SRP、受信任设备验证复用现有认证实现，但邮箱独立保存 `primary-mail-session.json`，不复用隐藏邮件地址的资格限制。未知挑战、缺少安全问题答案、条款和 Apple 限制会显示为待处理，不冒充已完成。网页里完成验证不会直接向本程序转移浏览器 cookie；必要时需重新连接，让本程序重新建立会话。

网页认证按 Apple 返回的 SRP 协议派生密码证明：`s2k` 使用 SHA-256 原始摘要，`s2k_fo` 使用摘要的小写十六进制文本，再执行 PBKDF2。不能把两种协议都按 `s2k` 处理，否则正确密码也可能得到 HTTP 401。账号管理网页共用该派生实现；未知协议或异常迭代次数会停止认证，不提交密码证明。兼容性参考 [PyiCloud SRP 实现](https://pyicloud.readthedocs.io/en/2026.10.0/api/pyicloud-service/)。

一次网页登录的授权查询参数和后续请求头使用相同的 frame/state 标识，客户端信息在该次握手中保持一致；Origin 仅包含协议和主机。SRP 初始化的非成功响应只报告阶段、HTTP 状态和数字错误码，不记录认证响应内容。

遇到网页登录完成阶段返回 `HTTP 412`、`authType=sa` 时，程序读取具体题目并使用[本机加密保存的安全问题答案](security-questions.md)完成验证。缺少答案时在面板填写后继续；不将其误报为密码错误。正常的设备登录可继续保持在线。

## 重启与不确定结果

创建前先持久化 `submitted` 标记，保存失败不发请求。创建最多提交一次，随后回读 mail 服务。即使 HTTP 成功，也必须回读确认；即使网络失败，也不能认定 Apple 未执行。未确认的任务跨重启保留，只检查状态，不换名字重复创建。遇到无法确认的拒绝，请先到官方邮箱页核实，面板保留“检查邮箱状态”。

新账号任务每次成功连接最多执行一次，不在定时续登录中循环重试。邮箱任务失败独立显示，不把正常账号标成离线。任务和网页会话只保存在被忽略的账号状态目录中，不提交 Git。

## 验证

`go test ./icloud ./cmd/trusted-device ./internal/dashboard ./internal/control ./internal/mailsetup` 覆盖已有邮箱跳过、名称占用、账号绑定、响应缺失、创建前落盘失败、丢失响应回读及跨进程防重提。`tools/test-primary-mail-ui.cjs` 使用离线页面/接口夹具检查面板状态与交互。

这些测试不等于真实新账号入箱验收。Apple 私有网页接口可能变化；新账号实际创建还取决于 Apple 接受网页认证及该账号的开通资格。

2026-11-14 只读实测：对已经在官网创建邮箱的授权账号，修复后的网页认证进入双重验证，本机设备码通过；随后查询确认 mail 服务存在。另一次由运行中面板发起的“检查邮箱”复用了保存的网页会话，状态更新为 ready。未调用邮箱创建、购买或兑换接口；未完成新版后台重启验收。
