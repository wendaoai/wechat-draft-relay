# 公众号草稿服务自托管

狐文或你的后端把正文和图片发给你部署的中转服务，由它调用微信，草稿进入你的公众号草稿箱。Docker 入口启用草稿创建、查询、重试、取消及连接检查。草稿写入所需的 AppID 和 AppSecret 由调用方提交。服务仅在处理时使用 AppSecret，正文、图片、任务状态和日志保存在你控制的 Docker 卷中。任务达到终态 24 小时后过期，由后台清理。

## 从源码构建

在仓库根目录执行以下命令。初始化会在数据卷内生成服务访问密钥，已有配置会被保留。

```bash
docker compose build relay
docker compose run --rm relay --init-config
docker compose up -d relay
curl --fail http://127.0.0.1:8201/readyz
```

## 接入狐文或自有应用

1. 为中转配置 HTTPS 域名，将请求代理到本机 `127.0.0.1:8201`。直连服务使用域名根地址。如果代理配置了路径前缀，填写带前缀的根地址，并由代理在转发时去掉前缀。反代需要允许最多 256 MiB 的 multipart 请求及约 130 秒的响应超时。
2. 在公众号管理后台将**中转服务器访问微信时的公网出口 IP**加入白名单。工作电脑的动态 IP 不影响远程中转。如果服务器的公网出口也会变化，需要更新微信白名单或使用固定出口。
3. 通过 `docker compose exec relay sed -n '/^WECHAT_DRAFT_SERVICE_KEY=/p' /state/.env` 获取自己的服务密钥。将 URL 和密钥填写到狐文公众号设置中的推送服务器配置，或按 [API 对接指南](wechat-draft-quickstart.md) 从自己的后端调用。调用时填写你的 API 根地址。

Docker 默认只向宿主机的回环地址发布端口。若反向代理也在容器里，可让它与中转加入同一个 Docker 网络，并代理到 `relay:8201`。请限制卷的访问权限。`docker compose down` 保留数据，带 `--volumes` 会删除数据卷和其中的服务密钥、任务及日志。
