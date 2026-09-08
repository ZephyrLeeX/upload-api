# Integration checks

Start the service as described in the project README, then run:

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

The two hashes must match. Repeat with representative 100 MiB and 1 GiB files before production rollout, then perform the Windows-to-Nginx end-to-end check from the main README.
