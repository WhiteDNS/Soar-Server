#!/usr/bin/env bash
# Builds the panel image from a verified bundle and checks that it initializes
# from stdin, binds privileged DNS as the unprivileged user and runs in
# container mode. Usage: docker_smoke_test.sh BUNDLE_DIR ARCH
set -euo pipefail
bundle=$1 arch=$2
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
name=whitedns-panel-smoke-$$
volume=$name-data
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
mkdir -p "$work/bin/linux-$arch"
cp "$bundle/bin/linux-$arch/soar-panel" "$work/bin/linux-$arch/soar-panel"
docker build --platform "linux/$arch" -f "$root/docker/Dockerfile" -t "$name:test" "$work"
[[ $(docker image inspect --format '{{.Config.User}}' "$name:test") == 65532:65532 ]] || { echo 'image must run as 65532' >&2; exit 1; }
docker volume create "$volume" >/dev/null
password=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')
printf '%s' "$password" | docker run --rm -i -v "$volume:/var/lib/soar-panel" "$name:test" \
  init --zone t.example.org --password-file - >/dev/null
# 127.0.0.1:53 proves the file capability survived the image build: the
# process runs unprivileged, and systemd-resolved holds only 127.0.0.53:53.
docker run -d --name "$name" --network host -v "$volume:/var/lib/soar-panel" "$name:test" \
  run --container --db /var/lib/soar-panel/panel.db --dns-listen 127.0.0.1:53 --listen 127.0.0.1:2054 >/dev/null
for _ in $(seq 30); do
  docker exec "$name" /usr/local/bin/soar-panel health --db /var/lib/soar-panel/panel.db >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$name" /usr/local/bin/soar-panel health --db /var/lib/soar-panel/panel.db || { docker logs "$name" >&2; exit 1; }
ss -H -lun | awk '$4=="127.0.0.1:53"{found=1}END{exit !found}' || { echo 'UDP DNS listener missing' >&2; docker logs "$name" >&2; exit 1; }
ss -H -ltn | awk '$4=="127.0.0.1:53"{found=1}END{exit !found}' || { echo 'TCP DNS listener missing' >&2; exit 1; }
jar=$work/cookies
curl -fsS -c "$jar" -H 'Content-Type: application/json' \
  --data "{\"name\":\"admin\",\"password\":\"$password\"}" http://127.0.0.1:2054/api/login >/dev/null
state=$(curl -fsS -b "$jar" http://127.0.0.1:2054/api/maintenance)
grep -q '"container":true' <<<"$state" || { echo "container mode missing: $state" >&2; exit 1; }
grep -q '"available":false' <<<"$state" || { echo "maintenance must be unavailable in a container: $state" >&2; exit 1; }
echo "PASS: $arch image initializes from stdin, binds DNS 53 as 65532 and runs in container mode."
