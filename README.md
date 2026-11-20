# WeChat Draft Relay

自托管的微信公众号草稿中转服务。调用方将文章和图片提交给自己的中转服务器，由中转调用微信。推送成功后返回 `mediaId`，可在公众号后台打开草稿。

## 快速启动

```bash
docker compose build relay
docker compose run --rm relay --init-config
docker compose up -d relay
curl --fail http://127.0.0.1:8201/readyz
```

初始化时会在 Docker 卷内生成服务密钥。远程接入时使用 HTTPS，并将中转服务器的公网出口 IP 加入公众号 IP 白名单。正文、图片和任务记录保存在自建服务器的数据卷内。

- [部署和狐文接入](docs/wechat-draft-self-hosting.md)
- [HTTP API 和 curl 示例](docs/wechat-draft-quickstart.md)

## 开发验证

```bash
go test ./...
```

测试用本地模拟微信和临时任务目录。
