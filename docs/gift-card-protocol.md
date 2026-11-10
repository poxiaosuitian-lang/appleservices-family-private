# 礼品卡协议兑换

实现不启动或控制 Apple Music，不读取官方客户端的 Cookie、缓存或钥匙串。使用项目自身的 Store 登录会话和内存 Cookie jar。

Windows 可将已认证的 Store 会话用当前 Windows 用户的 DPAPI 加密保存，重启后先向 Apple 做只读核验，再继续准备兑换页；详见[自动恢复登录](automatic-login.md)。验证码、礼品卡代码及兑换确认 ID 不保存。密码仅在用户选择“加密记住密码”时单独加密保存，用于会话失效后重新认证。

## 已验证与待验证

- 独立协议登录后，实时读取桌面兑换页成功；页面包含兑换输入框，显示的账号与登录账号相符。
- 当前页面 `redeemForm` 的 `submit-url` 指向 `/commerce/redeemCodeSrv`。公开的 `desktop-finance-redeemcode.js` 给出了表单 POST 参数。之前直接访问现代 `redeemInfo` / `codeDetails` 得到 401，不能据此判定整个协议兑换不可行，也不能据桌面页可读就宣称充值已成功。
- 代码已实现表单提交与结果解析，测试使用本地合成响应。**尚未使用新的、未兑换礼品卡完成真实提交验收**；已兑换过的卡不自动重放。

## 调用顺序

1. `gift-prepare`：按需建立项目自己的 Store 会话，GET `https://buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/redeemLandingPage`。保留响应 Cookie，核对页面账号、兑换输入框和唯一的提交表单。
2. 返回 `gift_card.preparation_id`，有效期五分钟，仅用于当前会话的一次确认。预检查不接收卡码，也不检查卡是否有效或读取余额。
3. 用户确认后发送 `gift-redeem`，`code` 为充值礼品卡代码，`request_id` 为上一步的确认 ID。
4. 同一 Cookie jar 向页面给出的允许列表端点提交 `application/x-www-form-urlencoded`：`code`、`dsPersonId`、`response-content-type`、`cameraRecognizedCode`、`has4GBLimit`、`attemptCount`、`guid`、`cl`、`pg`。账号标识和设备身份仅从已绑定会话获得。
5. 仅明确 `status=0`、无错误编号、无商品/订阅结果且含入账金额和余额时返回 `completed`。如果响应包含账号，则再次核对。HTTP 200、空响应或未知结构均不是成功。
6. 兑换后可继续现有 iCloud+ 预检查与用户确认购买流程；兑换动作本身不自动购买订阅。

## 重复提交与隐私

确认 ID 在发送前消耗。同一 Store 会话在内存中记录已提交代码的 SHA-256 摘要，重复请求不会再次发送；不写入文件。重启或更换 Store 会话后，这份内存记录会消失，因此结果不明时必须先核对官方余额，不能将重新连接当作重试依据。

不自动跟随跳转、不重试 POST、不自动确认 Apple 的额外对话框或购买动作。只接受 16 位、X 开头的充值礼品卡格式；格式检查不保证卡片类型、有效性或地区正确性，不用于订阅优惠码。

兑换码仅短暂存在输入框和请求内存中，提交时清空输入框，不放入 URL、浏览器存储、日志或诊断文件。接口只返回状态、金额及数字错误编号，不公开账号、Apple token、Cookie 或原始响应。

主要实现：`icloud/gift_card.go`、`gift_card.go`、`cmd/trusted-device/main.go` 及面板。测试：`go test ./icloud -run Gift`，以及 `tools/test-icloud-purchase-ui.cjs` 的离线浏览器回归。

协议依据为 Apple 当次实时提供的桌面兑换页与[公开脚本](https://buy.itunes.apple.com/htmlResources/7441/desktop-finance-redeemcode.js)。路径和页面契约变动时应重新核对，不盲目增加接口回退。

本机常规单元测试（含并发重复提交）与离线浏览器回归通过。额外的 `go test -race` 因已有 MinGW 工具链缺少 `WaitOnAddress` / `WakeByAddress*` 链接符号未能构建，未声称竞态检测通过。
