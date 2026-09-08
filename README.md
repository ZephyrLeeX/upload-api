# HTTP Upload API

A deliberately small, authenticated single-file upload service. It accepts a streaming HTTP `PUT`, validates an optional SHA256, writes to a same-filesystem temporary file, calls `fsync`, and atomically publishes the completed file without overwriting an existing name. V1 has no download, listing, deletion, database, UI, multipart upload, or resume support.

## Architecture

```text
Windows curl
  -> HTTP 10.9.195.133:50552 (network gateway)
  -> Internal Nginx :1080 (streaming reverse proxy)
  -> HTTPS 117.139.126.166:10443
  -> Go Upload API
  -> /data/uploads/.tmp -> /data/uploads/<filename>
```

The Windows-to-Nginx hop is HTTP. Nginx verifies the Upload API certificate with the private CA on the HTTPS hop. Authentication is always checked by the API using a fixed Bearer token.

## Build and test

The repository pins Go 1.24 in `mise.toml`:

```bash
mise install
mise exec -- go test ./...
mise exec -- go vet ./...
mkdir -p build
mise exec -- go build -trimpath -ldflags='-s -w' -o build/upload-api ./cmd/upload-api
```

Linux amd64 cross-build:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 mise exec -- \
  go build -trimpath -ldflags='-s -w' -o build/upload-api-linux-amd64 ./cmd/upload-api
```

## Configuration

| Variable | Default | Meaning |
|---|---:|---|
| `LISTEN_ADDR` | `:10443` | HTTPS listen address |
| `STORAGE_DIR` | `/data/uploads` | Final file directory |
| `MAX_FILE_SIZE` | `21474836480` | Maximum bytes (20 GiB) |
| `MAX_CONCURRENT_UPLOADS` | `1` | Immediate upload slots; administrators may explicitly configure more |
| `MIN_FREE_SPACE` | `1073741824` | Bytes that must remain free |
| `TEMP_FILE_TTL` | `24h` | Abandoned `.tmp` lifetime |
| `SHUTDOWN_TIMEOUT` | `2h` | Graceful shutdown wait for active uploads (Go duration) |
| `UPLOAD_TOKEN` | required | Secret of at least 32 characters |
| `TLS_CERT_FILE` | `/etc/upload-api/tls/server.crt` | Server certificate |
| `TLS_KEY_FILE` | `/etc/upload-api/tls/server.key` | Server private key |

All values and both TLS files are validated at startup. The storage directory and its `.tmp` child are created and checked for temporary-file and hard-link publication access. Unknown-length/chunked request bodies are rejected.

## TLS preparation

For development, OpenSSL is required:

```bash
./scripts/generate-dev-certs.sh 117.139.126.166 ./certs
```

The certificate contains SANs for the supplied server IP and `127.0.0.1`. Keep `upload-ca.key` offline. Install only `upload-ca.crt` on internal Nginx, and install `server.crt` plus `server.key` on the Upload API host. Generated keys and certificates are ignored by Git.

## Local run

```bash
./scripts/generate-dev-certs.sh 127.0.0.1 ./certs
export UPLOAD_TOKEN="$(openssl rand -hex 32)"
export STORAGE_DIR=/tmp/upload-api-data
export MIN_FREE_SPACE=0
export TLS_CERT_FILE="$PWD/certs/server.crt"
export TLS_KEY_FILE="$PWD/certs/server.key"
mise exec -- go run ./cmd/upload-api
```

In another shell:

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

## Production deployment

Create the dedicated account and directories:

```bash
useradd --system --home /var/lib/upload-api --shell /usr/sbin/nologin upload
mkdir -p /var/lib/upload-api /data/uploads/.tmp /etc/upload-api/tls
chown -R upload:upload /var/lib/upload-api /data/uploads
chown root:upload /etc/upload-api /etc/upload-api/tls
chmod 750 /etc/upload-api /etc/upload-api/tls /data/uploads /data/uploads/.tmp
```

Copy the binary to `/usr/local/bin/upload-api`, the example environment file to `/etc/upload-api/upload-api.env`, TLS files to `/etc/upload-api/tls`, and the systemd unit to `/etc/systemd/system/upload-api.service`. Set the environment file to `root:upload` mode `0640`, and the private key to `root:upload` mode `0640`. Then:

```bash
systemctl daemon-reload
systemctl enable --now upload-api
systemctl status upload-api
journalctl -u upload-api -f
```

Install `deploy/nginx/internal-upload.conf` on the internal proxy after copying the CA certificate to `/etc/nginx/certs/upload-ca.crt`. Remove the old WebDAV storage configuration, run `nginx -t`, and reload Nginx. The supplied configuration disables request buffering and restricts routes to health and upload.

At the network layer, allow TCP 10443 only from the known proxy public address (`124.161.60.222`) when possible.

## Windows upload

```powershell
$env:UPLOAD_TOKEN = "replace-with-the-server-token"
$hash = (Get-FileHash "C:\data\project-data.zip" -Algorithm SHA256).Hash.ToLower()

curl.exe -fS -T "C:\data\project-data.zip" `
  -H "Expect:" `
  -H "Authorization: Bearer $env:UPLOAD_TOKEN" `
  -H "Content-Type: application/octet-stream" `
  -H "X-File-SHA256: $hash" `
  "http://10.9.195.133:50552/api/v1/upload/project-data.zip"
```

## Responses

Success is `201 Created` with filename, received size, server-computed lowercase SHA256, and request ID. Errors are JSON and include a request ID.

| HTTP | Error | Meaning |
|---:|---|---|
| 400 | `invalid_filename`, `invalid_sha256`, `invalid_size` | Invalid request metadata |
| 401 | `unauthorized` | Bearer token missing or wrong |
| 409 | `file_exists` | Existing or in-progress same name |
| 411 | `length_required` | Missing Content-Length |
| 413 | `file_too_large` | Configured limit exceeded |
| 415 | `invalid_content_type` | Content-Type is not `application/octet-stream` |
| 422 | `size_mismatch`, `sha256_mismatch` | Integrity validation failed |
| 429 | `too_many_uploads` | No concurrency slot available |
| 500 | `internal_error` | Server-side failure |
| 507 | `insufficient_storage` | Required free space unavailable |

## Storage, cleanup, and token rotation

Only completed files appear directly under `STORAGE_DIR`. Active uploads use `STORAGE_DIR/.tmp/*.tmp`; failures remove their temporary file. Files older than `TEMP_FILE_TTL` are cleaned at startup and hourly. The service never cleans final files and never overwrites a name.

Rotate the V1 token by replacing `UPLOAD_TOKEN` in `/etc/upload-api/upload-api.env`, updating the Windows client secret, and restarting the service. V1 does not accept two tokens during a transition. Never put the real token in Git or command logs.

## Troubleshooting

- `401`: confirm the client and environment file use the same token; do not print it into shared logs.
- `409`: the final name already exists or the same name is currently uploading. Choose a new name; V1 cannot overwrite.
- `411`: ensure curl uses `-T` and sends a known file length; chunked uploads are unsupported.
- `413`: compare client size with `MAX_FILE_SIZE` and Nginx `client_max_body_size`.
- `422`: recompute SHA256 and confirm no intermediary transforms the body; a short connection also yields a size mismatch.
- `429`: wait for an active upload to finish or deliberately adjust `MAX_CONCURRENT_UPLOADS`.
- `507`: free disk capacity or adjust `MIN_FREE_SPACE` only after reviewing operational headroom.
- TLS errors at Nginx: verify the CA file, server certificate SAN `IP:117.139.126.166`, file permissions, and system clock.
- Interrupted upload residue: check permissions on `.tmp`, then inspect `journalctl -u upload-api`; the startup/hourly cleanup is a fallback.
