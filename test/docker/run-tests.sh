#!/bin/sh
# Entry point of the mek-test image: copy the read-only source mount into a
# writable workspace owned by the test user, then run the given make targets
# (default: test-all cover).
set -eu

rsync -a --delete --exclude /bin --exclude /dist /src/ /home/mek/mek/
cd /home/mek/mek
printf '%s' "$(go env GOVERSION)"
for c in aws gcloud az hcloud; do
  if command -v "$c" >/dev/null; then printf ' · %s ok' "$c"; else printf ' · %s MISSING' "$c"; fi
done
printf '\n'
exec make "$@"
