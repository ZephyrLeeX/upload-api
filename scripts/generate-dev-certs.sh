#!/usr/bin/env bash
set -euo pipefail

server_ip="${1:-117.139.126.166}"
output_dir="${2:-./certs}"
mkdir -p "$output_dir"

openssl genrsa -out "$output_dir/upload-ca.key" 4096
openssl req -x509 -new -sha256 -days 3650 \
  -key "$output_dir/upload-ca.key" \
  -subj "/CN=Upload API Development CA" \
  -out "$output_dir/upload-ca.crt"

openssl genrsa -out "$output_dir/server.key" 3072
openssl req -new -sha256 \
  -key "$output_dir/server.key" \
  -subj "/CN=$server_ip" \
  -addext "subjectAltName=IP:$server_ip,IP:127.0.0.1" \
  -out "$output_dir/server.csr"

extension_file="$(mktemp)"
trap 'rm -f "$extension_file"' EXIT
printf 'subjectAltName=IP:%s,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' "$server_ip" > "$extension_file"
openssl x509 -req -sha256 -days 825 \
  -in "$output_dir/server.csr" \
  -CA "$output_dir/upload-ca.crt" \
  -CAkey "$output_dir/upload-ca.key" \
  -CAcreateserial \
  -extfile "$extension_file" \
  -out "$output_dir/server.crt"
chmod 600 "$output_dir"/*.key
echo "Certificates generated in $output_dir for IP $server_ip"
