package wechatdraft

// Initialization and the documented example contain the same active fields.
// Keep legacy account settings commented: storing them does not enable auth.
func defaultEnvText(key string) string {
	return `# Private program-local .env; do not commit secrets.
# Read beside the executable; source: go run ./cmd/wechat-draft --config ./.env
# Restart after edits. No system environment, interpolation or multiline values.

# Shared HTTP address. :8201 listens on all local interfaces.
# For local-only access, use 127.0.0.1:8201.
# WECHAT_DRAFT_LISTEN is a compatibility alias; do not configure both differently.
GF_SERVER_ADDRESS=:8201

# WeChat draft service: private random key, never a JWT secret or AppSecret.
# --init-config generates 32 random bytes encoded as 43 base64url characters.
WECHAT_DRAFT_SERVICE_KEY=` + key + `
WECHAT_DRAFT_DATA_DIR=data/wechat-drafts
WECHAT_DRAFT_WORKERS=2
WECHAT_DRAFT_QUEUE_SIZE=4
WECHAT_DRAFT_UPLOAD_CONCURRENCY=4
WECHAT_DRAFT_QUERY_RATE_PER_SECOND=100
WECHAT_DRAFT_IP_WRITE_LIMIT_PER_MINUTE=120
WECHAT_DRAFT_IP_QUERY_LIMIT_PER_MINUTE=6000
WECHAT_DRAFT_IP_CANCEL_LIMIT_PER_MINUTE=600
WECHAT_DRAFT_CLEANUP_INTERVAL_SECONDS=300
WECHAT_DRAFT_DISK_QUOTA_BYTES=5368709120

# File limit: 64 KiB, UTF-8 (optional BOM), one assignment per line.
# Quotes/comments/export supported; no escape decoding or variable expansion.
# Duplicate/unknown WECHAT_DRAFT fields fail; unrelated legacy fields ignored.
# Shared isolated load profile: WORKERS=32, QUEUE_SIZE=64.
`
}
