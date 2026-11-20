# WeChat Draft Relay

[![微信公众号 草稿推送](https://img.shields.io/badge/%E5%BE%AE%E4%BF%A1%E5%85%AC%E4%BC%97%E5%8F%B7-%E8%8D%89%E7%A8%BF%E6%8E%A8%E9%80%81-07C160?style=flat-square)](docs/wechat-draft-quickstart.md) [![数据 自托管](https://img.shields.io/badge/%E6%95%B0%E6%8D%AE-%E8%87%AA%E6%89%98%E7%AE%A1-0F766E?style=flat-square)](docs/wechat-draft-self-hosting.md) [![HTTP API 第三方接入](https://img.shields.io/badge/HTTP%20API-%E7%AC%AC%E4%B8%89%E6%96%B9%E6%8E%A5%E5%85%A5-2563EB?style=flat-square)](docs/wechat-draft-quickstart.md) [![狐文工作台 支持接入](https://img.shields.io/badge/%E7%8B%90%E6%96%87%E5%B7%A5%E4%BD%9C%E5%8F%B0-%E6%94%AF%E6%8C%81%E6%8E%A5%E5%85%A5-8B5CF6?style=flat-square)](docs/wechat-draft-self-hosting.md#接入狐文或自有应用)

[![GoFrame v2](https://img.shields.io/badge/GoFrame-v2-00ADD8?style=flat-square&logo=go&logoColor=white)](go.mod) [![Docker Compose 部署](https://img.shields.io/badge/Docker%20Compose-%E9%83%A8%E7%BD%B2-2496ED?style=flat-square&logo=docker&logoColor=white)](#快速启动) [![开源协议 MIT](https://img.shields.io/badge/%E5%BC%80%E6%BA%90%E5%8D%8F%E8%AE%AE-MIT-D99428?style=flat-square)](LICENSE)

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
