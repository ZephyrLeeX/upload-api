# 集成测试

按照项目主 README 的说明启动服务，然后执行：

```bash
curl --cacert ./certs/upload-ca.crt https://127.0.0.1:10443/health

dd if=/dev/urandom of=/tmp/1m.bin bs=1M count=1
HASH=$(sha256sum /tmp/1m.bin | awk '{print $1}')
curl --cacert ./certs/upload-ca.crt -fS -T /tmp/1m.bin \
  -H 'Expect:' \
  -H "Authorization: Bearer $UPLOAD_TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  -H "X-File-SHA256: $HASH" \
  https://127.0.0.1:10443/api/v1/upload/1m.bin

sha256sum /tmp/1m.bin /tmp/upload-api-data/1m.bin
```

两个文件的哈希值必须一致。生产发布前，请分别使用具有代表性的 100 MiB 和 1 GiB 文件重复测试，然后按照主 README 的说明执行 Windows 到 Nginx 的端到端检查。
