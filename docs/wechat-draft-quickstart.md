# 微信公众号草稿推送 API 对接指南

调用方可以通过 HTTPS 将文章和图片提交到指定的**微信公众号草稿箱**。任务成功后返回微信草稿 `mediaId`，运营人员可在公众号后台打开草稿并发布。

将下列路径拼接到部署方提供的 API 根地址。本机按默认 Compose 配置部署后，调试地址为 `http://127.0.0.1:8201`。远程调用请使用部署方配置的 HTTPS 地址。

## 接入准备

- **服务访问密钥**由服务提供方通过安全渠道交付给获授权的后端对接方。业务请求统一带 `Authorization: Bearer <服务访问密钥>`。
- **公众号 AppID 和 AppSecret** 由公众号管理员提供。AppID 使用 `wx` 加 16 位小写十六进制字符。公众号需具备素材及草稿写入权限，微信侧如开启 IP 白名单，需包含服务端访问微信的出口 IP。
- 服务访问密钥、AppSecret 和任务 Token 由后端保管，请勿放进网页脚本、URL 或日志。业务接口不带 URL 查询参数。

| 方法 | 路径 | 成功响应 |
| --- | --- | --- |
| `GET` | `/readyz` | `200`，`{"status":"ok"}`，无需鉴权 |
| `GET` | `/api/v1/wechat-drafts/service-check` | `200`，`data.ready=true` |
| `POST` | `/api/v1/wechat-drafts/connection-test` | `200`，`data.connected=true` |
| `POST` | `/api/v1/wechat-drafts` | 首次受理 `202`，同任务同稿件重放 `200` |
| `GET` | `/api/v1/wechat-drafts/{appid}/{uuid}` | `200`，返回任务状态 |
| `POST` | `/api/v1/wechat-drafts/{appid}/{uuid}/retry` | 新轮次受理 `202`，同操作重放 `200` |
| `POST` | `/api/v1/wechat-drafts/{appid}/{uuid}/cancel` | `200`，返回当前任务状态 |

`connection-test` 使用 `Content-Type: application/json`，请求字段为 `{"appid":"wx0123456789abcdef","appsecret":"<公众号 AppSecret>"}`，整个请求体最多 4096 字节。它只验证服务端能否取得微信访问令牌。`service-check` 和 `connection-test` 都只需要服务鉴权头。实际草稿写入可用测试公众号验证。

## 1. 创建图文草稿

`POST /api/v1/wechat-drafts` 使用 `multipart/form-data`。下面是各个 part 的发送顺序。

1. 首个 part 是 `payload`，内容为 UTF-8 JSON 文本，不带 `filename`。
2. 后续每个 part 对应 `images` 中的一项，字段名为 `file_<图片 ID>`，内容为图片文件原始字节。每个字段发送一次。

创建、重试和取消还需 `X-Wechat-AppId`、`X-Draft-UUID` 和 `X-Draft-Token`。AppID、UUID 在请求头、路径及 JSON 中出现时必须一致。新任务使用当前时间生成的小写 UUID v7，允许的时间窗口为过去 5 分钟至未来 30 秒。任务 Token 由调用方用 32 个安全随机字节生成，按无填充的 base64url 编码，长度为 43 字符。调用方应在提交前保存任务身份和稿件快照，以便查状态和恢复断线请求。同一任务始终使用原 Token。

| `payload` 字段 | 说明 |
| --- | --- |
| `schemaVersion` | 整数，普通图文用 `1`，图片消息用 `2` |
| `appid`、`uuid`、`appsecret` | 创建时发送三个字符串，AppSecret 最多 256 字节 |
| `article.title`、`article.contentHtml` | 必填字符串，标题非空，正文为非空 HTML 片段 |
| `article.author`、`article.digest` | 选填字符串，分别为作者和摘要 |
| `article.contentSourceUrl` | 选填字符串，最多 1024 字节的 HTTP(S) URL，不带用户名或密码 |
| `article.needOpenComment`、`article.onlyFansCanComment` | 选填整数，`0` 或 `1`，默认 `0`。关闭评论时仅粉丝可评论也归为 `0` |
| `coverImageId` | 必填字符串，指向 `images` 中唯一的 `cover` 项 |
| `images` | 必填数组，每项包含 `id`、`role`、`fileField`、`sha256` 四个字符串。`role` 为 `cover` 或 `body`，`id` 允许 1–64 个字母、数字、下划线或短横线，`fileField` 为 `file_` 加 `id`，`sha256` 为上传文件的 64 位小写十六进制 SHA-256 |

正文插图使用 `<img src="cw-image://photo1" alt="示例">`，同时声明一项 `id=photo1`、`role=body`、`fileField=file_photo1`，再上传对应文件。每个 `body` 项都需要在正文中被引用。封面支持 PNG/JPEG，正文图片支持 PNG/JPEG/GIF。正文传片段和内联样式，不带完整 HTML 文档、脚本、SVG 或远程图片地址。单次 HTTP 请求总量最多 256 MiB。

普通图文省略 `article.articleType` 或传 `news`。图片消息使用 `schemaVersion=2`、`article.articleType="newspic"` 和 `article.imageIds`，后者是按展示顺序排列的 1–20 个 `body` 图片 ID。首张展示图用作微信侧封面，上传清单仍需单独提供一项 `cover`。`article.contentHtml` 仍需引用正文图片，`article.digest` 用作图片消息说明。

### 可复制示例

在同一个 Bash 会话中依次执行各段。示例需要 `curl`、Python 3.7 或更高版本，以及当前目录下的 `cover.jpg`、`article.html`。将 `DRAFT_TITLE` 改为实际标题。示例正文可先用 `<p>这是一篇测试稿件。</p>`，便于验证仅有封面的图文请求。

```bash
read -r -p 'API 根地址（例如 http://127.0.0.1:8201）: ' DRAFT_BASE_URL
DRAFT_API="${DRAFT_BASE_URL%/}/api/v1/wechat-drafts"
DRAFT_TITLE='接口对接示例'
read -r -p '公众号 AppID: ' WX_APPID
read -r -s -p '服务访问密钥: ' DRAFT_SERVICE_KEY; printf '\n'
read -r -s -p '公众号 AppSecret: ' WX_APPSECRET; printf '\n'

service_headers() {
  printf 'Authorization: Bearer %s\n' "$DRAFT_SERVICE_KEY"
}

task_headers() {
  service_headers
  printf 'X-Draft-Token: %s\n' "$DRAFT_TOKEN"
}

write_headers() {
  task_headers
  printf 'X-Wechat-AppId: %s\nX-Draft-UUID: %s\n' "$WX_APPID" "$DRAFT_UUID"
}

curl -sS -i -H @<(service_headers) "$DRAFT_API/service-check"
```

下面的命令会真实提交草稿。为一次全新推送生成身份，完成稿件准备后执行。网络重发只复用 `send_draft`、原身份和原文件。

```bash
read -r DRAFT_UUID DRAFT_TOKEN < <(python3 - <<'PY'
import secrets
import time
import uuid

timestamp_ms = time.time_ns() // 1_000_000
value = (timestamp_ms << 80) | (7 << 76) | (secrets.randbits(12) << 64)
value |= (2 << 62) | secrets.randbits(62)
print(uuid.UUID(int=value), secrets.token_urlsafe(32))
PY
)
TASK_URL="$DRAFT_API/$WX_APPID/$DRAFT_UUID"

send_draft() {
  local endpoint="$1" payload
  payload=$(WX_APPSECRET="$WX_APPSECRET" python3 - "$WX_APPID" "$DRAFT_UUID" \
    "$DRAFT_TITLE" "${2:-}" "${3:-}" <<'PY'
import hashlib
import json
import os
from pathlib import Path
import sys

appid, task_uuid, title, expected_attempt, retry_request_id = sys.argv[1:]
payload = {
    "schemaVersion": 1,
    "appid": appid,
    "uuid": task_uuid,
    "appsecret": os.environ["WX_APPSECRET"],
    "article": {"title": title, "contentHtml": Path("article.html").read_text(encoding="utf-8")},
    "coverImageId": "cover",
    "images": [{
        "id": "cover",
        "role": "cover",
        "fileField": "file_cover",
        "sha256": hashlib.sha256(Path("cover.jpg").read_bytes()).hexdigest(),
    }],
}
if expected_attempt:
    payload.update(expectedAttempt=int(expected_attempt), retryRequestId=retry_request_id)
print(json.dumps(payload))
PY
  ) || return

  printf '%s' "$payload" | curl -sS -i -H @<(write_headers) \
    -F 'payload=<-' -F 'file_cover=@cover.jpg;type=image/jpeg' "$endpoint"
}

send_draft "$DRAFT_API"
```

## 2. 查询结果

`202` 表示任务已受理。收到创建响应后使用原 AppID、UUID 和 Token 查询。GET 只需服务鉴权头与任务 Token，AppID 和 UUID 从路径获取。

```bash
curl -sS -i -H @<(task_headers) "$TASK_URL"
```

业务响应形如 `{"data":{...},"requestId":"..."}`，请求失败时形如 `{"error":{"code":"...","message":"..."},"requestId":"..."}`。记录 `requestId` 便于排查。任务失败仍可能返回 HTTP `200`，此时错误位于 `data.error`。

终态成功的 HTTP `200` 响应包含以下字段（节选，`mediaId` 为示例值）。

```json
{
  "data": {
    "status": "succeeded",
    "phase": "done",
    "attempt": 1,
    "progress": {"completed": 4, "total": 4, "percent": 100},
    "result": {"mediaId": "example_draft_media_id", "completedAt": "2026-11-20T14:00:00+08:00"},
    "error": null
  },
  "requestId": "7b1d534b-290a-4189-875a-b95f86b5bffa"
}
```

| `data.status` | 调用方处理 |
| --- | --- |
| `queued` / `running` | 按 `data.pollAfterSeconds` 轮询，排队通常为 7 秒，执行中通常为 3 秒 |
| `succeeded` | 确认 `phase="done"`、`progress.percent=100`、`result.mediaId` 非空且有 `result.completedAt` 后，记录推送成功和草稿 `mediaId` |
| `failed` | 读取 `data.error.code`，按 `data.retryAllowed` 决定是否提供重试 |
| `cancelled` | 停止轮询 |
| `uncertain` | 停止写操作，到公众号草稿箱核对。用新 UUID 重新提交可能造成重复草稿 |

终态任务可查询 24 小时，建议记录 `data.expiresAt` 和成功返回的 `mediaId`。到期清理的是中转服务的任务记录，微信草稿仍保留。响应时间为带 `+08:00` 偏移的 ISO 8601 时间。

## 3. 重试、取消和断线恢复

只有最新查询返回 `status=failed` 且 `retryAllowed=true` 时才发起新一轮重试。沿用原任务身份、稿件清单和图片，发送到 `/retry`。`payload` 增加整数 `expectedAttempt`（取自最新 `data.attempt`）和字符串 `retryRequestId`（本次操作新生成的小写 UUID v4）。

```bash
read -r -p '最新查询返回的 data.attempt: ' EXPECTED_ATTEMPT
RETRY_REQUEST_ID=$(python3 -c 'import uuid; print(uuid.uuid4())')
send_draft "$TASK_URL/retry" "$EXPECTED_ATTEMPT" "$RETRY_REQUEST_ID"
```

取消前确认 `data.cancelAllowed=true`，使用最新 `data.attempt`。HTTP `200` 时若任务仍在运行，继续查询实际状态。进入提交微信阶段后，取消会返回 `409 CANCEL_TOO_LATE`。

```bash
read -r -p '最新查询返回的 data.attempt: ' EXPECTED_ATTEMPT
printf '{"expectedAttempt":%s}' "$EXPECTED_ATTEMPT" | curl -sS -i \
  -H @<(write_headers) -H 'Content-Type: application/json' \
  --data-binary @- "$TASK_URL/cancel"
```

- **创建响应丢失**时，先 GET 原任务。`200` 就接续原任务。若是 `404`，且确认 AppID、UUID、Token 正确、UUID 仍在新建窗口内，可有限次重发原身份和完整请求。过窗后先核对微信草稿箱。
- **重试响应丢失**时，先 GET 原任务。`lastRetry.requestId` 等于本次 `retryRequestId` 表示该操作已生效。若仍处于原 `attempt` 且允许重试，可有限次重发同一 `expectedAttempt`、`retryRequestId` 和完整请求。查询失败、状态未知或轮次已被其他操作推进时，暂停继续写入。
- **取消响应丢失**时，先 GET 原任务。仍在同一轮次且允许取消时，可重发原取消请求。

## 常见错误

| HTTP / 错误码 | 处理 |
| --- | --- |
| `401 SERVICE_AUTH_REQUIRED` | 检查服务访问密钥 |
| `404 TASK_NOT_FOUND` | 核对原 AppID、UUID、Token 和有效期 |
| `413 PAYLOAD_TOO_LARGE` | 核对 multipart 总大小 |
| `422 VALIDATION_FAILED`、`IMAGE_REFERENCE_INVALID`、`IMAGE_INVALID`、`IMAGE_HASH_MISMATCH` | 核对 JSON、HTML 图片引用、文件编码或上传字节摘要 |
| `422 UUID_TIME_INVALID` | 按 `error.details.serverTime` 检查调用方时钟 |
| `409 IDEMPOTENCY_CONFLICT` | 本 UUID 已绑定另一份稿件清单，恢复时须使用原内容 |
| `409 CREATE_IN_PROGRESS`、`RETRY_IN_PROGRESS`、`ATTEMPT_CONFLICT` | 查原任务及当前轮次，有 `Retry-After` 时按其秒数等待 |
| `409 REQUEST_EXPIRED`、`TASK_RECOVERY_REQUIRED` | 暂停写入并核对微信草稿箱 |
| `429`、`503` | 遵守响应中的 `Retry-After`，写入返回异常时先查询任务，再决定是否重发同一请求 |
| `data.error.code=WECHAT_CREDENTIAL_INVALID`、`WECHAT_IP_NOT_ALLOWED`、`WECHAT_PERMISSION_DENIED` | 分别核查公众号凭据、微信侧 IP 白名单和账号权限 |
