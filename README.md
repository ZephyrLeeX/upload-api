# HTTP 文件上传 API

这是一个刻意保持精简、带身份认证的单文件上传服务。服务接收流式 HTTP `PUT` 请求，可选校验 SHA256，将内容写入同一文件系统中的临时文件，调用 `fsync` 后以原子方式发布完整文件，且绝不覆盖同名文件。V1 不提供下载、文件列表、删除、数据库、用户界面、multipart 上传或断点续传功能。

## 架构

```text
Windows curl
  -> HTTP 10.9.195.133:50552（网络网关）
  -> 内部 Nginx :1080（流式反向代理）
  -> HTTPS 117.139.126.166:10443
  -> Go 文件上传 API
  -> /data/upload-tmp/<request-id>-*.tmp -> /data/files/<filename>
```

Windows 到 Nginx 之间使用 HTTP。Nginx 在 HTTPS 链路上使用私有 CA 验证文件上传 API 的证书。API 始终使用固定的 Bearer 令牌进行身份认证。

## 构建与测试

仓库通过 `mise.toml` 固定使用 Go 1.24：

```bash
mise install
mise exec -- go test ./...
mise exec -- go vet ./...
mkdir -p build
mise exec -- go build -trimpath -ldflags='-s -w' -o build/upload-api ./cmd/upload-api
```

交叉构建 Linux amd64 版本：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 mise exec -- \
  go build -trimpath -ldflags='-s -w' -o build/upload-api-linux-amd64 ./cmd/upload-api
```

## 发布版本

推送以 `v` 开头的版本标签会触发发布工作流，例如：

```bash
git tag -a v1.0.0 -m "v1.0.0"
git push origin v1.0.0
```

工作流会先运行全部测试和静态检查，再生成 Linux amd64 静态二进制文件及其 SHA256 校验文件。两个文件既会保存为 GitHub Actions 构建产物，也会附加到对应的 GitHub Release。二进制文件名包含版本标签，例如 `upload-api-v1.0.0-linux-amd64`。

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---:|---|
| `LISTEN_ADDR` | `:10443` | HTTPS 监听地址 |
| `STORAGE_DIR` | `/data/uploads` | 最终文件存储目录 |
| `TMP_DIR` | `/data/upload-tmp` | 上传中的临时文件目录 |
| `MAX_FILE_SIZE` | `21474836480` | 最大文件大小，单位为字节（20 GiB） |
| `MAX_CONCURRENT_UPLOADS` | `1` | 可立即处理的上传数量；管理员可显式配置更大的值 |
| `MIN_FREE_SPACE` | `1073741824` | 上传后必须保留的可用空间字节数 |
| `TEMP_FILE_TTL` | `24h` | 废弃 `.tmp` 文件的保留时间 |
| `SHUTDOWN_TIMEOUT` | `2h` | 优雅关闭时等待活动上传完成的时间，使用 Go 时长格式 |
| `UPLOAD_TOKEN` | 必填 | 长度至少为 32 个字符的密钥 |
| `TLS_CERT_FILE` | `/etc/upload-api/tls/server.crt` | 服务端证书文件 |
| `TLS_KEY_FILE` | `/etc/upload-api/tls/server.key` | 服务端私钥文件 |

服务启动时会校验所有配置值和两个 TLS 文件。服务会分别创建 `STORAGE_DIR` 和 `TMP_DIR`，确认两者是同一文件系统上的不同目录，并检查临时文件写入和硬链接发布能力。V1 使用 hard link 原子发布已完成文件，因此目录相同或跨文件系统时服务会启动失败。服务拒绝长度未知或使用 chunked 编码的请求体。

## 准备 TLS 证书

开发环境需要安装 OpenSSL：

```bash
./scripts/generate-dev-certs.sh 117.139.126.166 ./certs
```

生成的证书包含指定服务端 IP 和 `127.0.0.1` 的 SAN。请离线保管 `upload-ca.key`。内部 Nginx 只需安装 `upload-ca.crt`；文件上传 API 主机需要安装 `server.crt` 和 `server.key`。Git 会忽略生成的密钥和证书。

## 本地运行

```bash
./scripts/generate-dev-certs.sh 127.0.0.1 ./certs
export UPLOAD_TOKEN="$(openssl rand -hex 32)"
export STORAGE_DIR=/tmp/upload-api-data
export TMP_DIR=/tmp/upload-api-tmp
export MIN_FREE_SPACE=0
export TLS_CERT_FILE="$PWD/certs/server.crt"
export TLS_KEY_FILE="$PWD/certs/server.key"
mise exec -- go run ./cmd/upload-api
```

在另一个终端中执行：

```bash
curl --cacert ./certs/upload-ca.crt https://127.0.0.1:10443/health
printf 'hello\n' >/tmp/hello.txt
HASH=$(sha256sum /tmp/hello.txt | awk '{print $1}')
curl --cacert ./certs/upload-ca.crt -fS -T /tmp/hello.txt \
  -H 'Expect:' \
  -H "Authorization: Bearer $UPLOAD_TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  -H "X-File-SHA256: $HASH" \
  https://127.0.0.1:10443/api/v1/upload/hello.txt
```

## 生产环境部署

创建共享组、专用账户和目录。如果 `filetransfer` 组已经存在，请跳过 `groupadd`；如果 `vftp` 用户已经存在，直接将其加入该组：

```bash
groupadd --system filetransfer
useradd --system \
  --home /var/lib/upload-api \
  --shell /usr/sbin/nologin \
  upload
usermod -aG filetransfer upload
usermod -aG filetransfer vftp

mkdir -p /var/lib/upload-api
mkdir -p /data/files
mkdir -p /data/upload-tmp
mkdir -p /etc/upload-api/tls

chown -R upload:upload /var/lib/upload-api
chown -R upload:filetransfer /data/files
chown -R upload:filetransfer /data/upload-tmp
chown root:upload /etc/upload-api /etc/upload-api/tls
chmod 750 /etc/upload-api /etc/upload-api/tls
chmod 2770 /data/files
chmod 2770 /data/upload-tmp
```

目录权限中的 `2xxx` 是 setgid 位，确保在这些目录中新建的文件和子目录继承 `filetransfer` 组。最终发布使用临时文件的硬链接，因此最终路径保持同一个 inode 及其 `filetransfer` 属组。应用不需要以 root 身份运行，也不需要在代码中执行 `chown`。

`STORAGE_DIR=/data/files` 保存最终完成文件，`TMP_DIR=/data/upload-tmp` 保存上传中的临时文件。两者必须是同一个文件系统上的不同目录。例如以下命令显示的设备号应相同：

```bash
df -T /data/files /data/upload-tmp
stat -c '%d %n' /data/files /data/upload-tmp
```

`/data/files` 与 `/data/upload-tmp` 在同一文件系统上是正确配置；如果 `/data/files` 与 `/mnt/other-disk/upload-tmp` 属于不同文件系统，则是错误配置，服务将拒绝启动。

启动服务前可验证用户组与目录权限：

```bash
id upload
id vftp
stat -c '%A %a %U %G %n' /data/files /data/upload-tmp
```

两个用户的输出都应包含 `filetransfer`。目录输出应类似：

```text
drwxrws--- 2770 upload filetransfer /data/files
drwxrws--- 2770 upload filetransfer /data/upload-tmp
```

将二进制文件复制到 `/usr/local/bin/upload-api`，将示例环境变量文件复制到 `/etc/upload-api/upload-api.env`，将 TLS 文件复制到 `/etc/upload-api/tls`，并将 systemd 单元文件复制到 `/etc/systemd/system/upload-api.service`。环境变量文件的所有者和权限应设置为 `root:upload`、`0640`；私钥也应设置为 `root:upload`、`0640`。然后执行：

```bash
systemctl daemon-reload
systemctl enable --now upload-api
systemctl status upload-api
journalctl -u upload-api -f
```

上传一个测试文件后，验证最终文件的权限与属组：

```bash
stat -c '%A %a %U %G %n' /data/files/test.bin
```

预期输出：

```text
-rw-rw---- 660 upload filetransfer /data/files/test.bin
```

将 CA 证书复制到 `/etc/nginx/certs/upload-ca.crt` 后，在内部代理上安装 `deploy/nginx/internal-upload.conf`。移除旧的 WebDAV 存储配置，运行 `nginx -t`，然后重新加载 Nginx。提供的配置会禁用请求缓冲，并将路由限制为健康检查和文件上传接口。

在网络层面，应尽可能仅允许已知代理公网地址（`124.161.60.222`）访问 TCP 10443 端口。

## Windows 上传

```powershell
$env:UPLOAD_TOKEN = "替换为服务器令牌"
$hash = (Get-FileHash "C:\data\project-data.zip" -Algorithm SHA256).Hash.ToLower()

curl.exe -fS -T "C:\data\project-data.zip" `
  -H "Expect:" `
  -H "Authorization: Bearer $env:UPLOAD_TOKEN" `
  -H "Content-Type: application/octet-stream" `
  -H "X-File-SHA256: $hash" `
  "http://10.9.195.133:50552/api/v1/upload/project-data.zip"
```

## 响应

上传成功时返回 `201 Created`，响应中包含文件名、实际接收大小、服务端计算的小写 SHA256 和请求 ID。错误响应使用 JSON 格式，并包含请求 ID。

| HTTP 状态码 | 错误码 | 含义 |
|---:|---|---|
| 400 | `invalid_filename`、`invalid_sha256`、`invalid_size` | 请求元数据无效 |
| 401 | `unauthorized` | Bearer 令牌缺失或错误 |
| 409 | `file_exists` | 同名文件已存在或正在上传 |
| 411 | `length_required` | 缺少 Content-Length |
| 413 | `file_too_large` | 超出配置的文件大小限制 |
| 415 | `invalid_content_type` | Content-Type 不是 `application/octet-stream` |
| 422 | `size_mismatch`、`sha256_mismatch` | 完整性校验失败 |
| 429 | `too_many_uploads` | 当前没有可用的并发上传槽位 |
| 500 | `internal_error` | 服务端内部错误 |
| 507 | `insufficient_storage` | 可用存储空间不足 |

## 存储、清理与令牌轮换

只有完整上传的文件才会直接出现在 `STORAGE_DIR` 下。进行中的上传使用 `TMP_DIR/*.tmp`；上传失败时会删除对应临时文件。服务只扫描 `TMP_DIR`，并在启动时和此后每小时清理早于 `TEMP_FILE_TTL` 的 `.tmp` 文件。两个目录必须配置为不同路径，以确保允许使用 `.tmp` 后缀的正式文件不会被清理。服务绝不会清理最终文件，也不会覆盖同名文件。

轮换 V1 令牌时，需要替换 `/etc/upload-api/upload-api.env` 中的 `UPLOAD_TOKEN`，同步更新 Windows 客户端使用的密钥，然后重启服务。V1 不支持在过渡期间同时接受两个令牌。绝不要将真实令牌写入 Git 或命令日志。

## 故障排查

- `401`：确认客户端与环境变量文件使用相同的令牌；不要将令牌输出到共享日志。
- `409`：最终文件已存在，或同名文件正在上传。请选择新文件名；V1 不支持覆盖。
- `411`：确保 curl 使用 `-T` 并发送已知文件长度；服务不支持 chunked 上传。
- `413`：比较客户端文件大小、`MAX_FILE_SIZE` 与 Nginx 的 `client_max_body_size`。
- `422`：重新计算 SHA256，并确认中间代理没有转换请求体；连接提前中断也会导致文件大小不匹配。
- `429`：等待当前上传完成，或在评估风险后调整 `MAX_CONCURRENT_UPLOADS`。
- `507`：释放磁盘空间；如需调整 `MIN_FREE_SPACE`，请先确认预留空间仍能满足运行要求。
- Nginx 报告 TLS 错误：检查 CA 文件、服务端证书中的 SAN `IP:117.139.126.166`、文件权限和系统时间。
- 上传中断后残留临时文件：检查 `TMP_DIR` 的权限，再查看 `journalctl -u upload-api`；启动时和每小时执行的清理任务是兜底机制。
