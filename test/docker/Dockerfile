# mek's test image: Go plus the four real cloud CLIs (aws, gcloud, az,
# hcloud), so every test layer runs the same on any machine. Built and run
# through compose.yaml (`make docker-test`); used by this project only.
FROM golang:1.27

LABEL org.opencontainers.image.title="mek-test" \
      org.opencontainers.image.source="https://github.com/prateep-r/mek" \
      io.github.prateep-r.mek="test"

RUN apt-get update -qq \
 && apt-get install -y -qq --no-install-recommends make rsync >/dev/null \
 && rm -rf /var/lib/apt/lists/*

COPY contract/install-clis.sh /tmp/install-clis.sh
RUN sh /tmp/install-clis.sh && rm /tmp/install-clis.sh

# Tests that check permission errors need a non-root user. The Go cache dir
# exists (owned by mek) before compose mounts its volume there, so the
# volume starts out writable by mek instead of root.
RUN useradd --create-home --uid 1000 mek \
 && mkdir -p /home/mek/go /home/mek/mek \
 && chown -R mek:mek /home/mek
USER mek
WORKDIR /home/mek/mek
ENV GOFLAGS=-buildvcs=false \
    GOPATH=/home/mek/go \
    AZURE_CORE_COLLECT_TELEMETRY=no \
    CLOUDSDK_CORE_DISABLE_USAGE_REPORTING=true

COPY --chown=mek docker/run-tests.sh /usr/local/bin/run-tests
ENTRYPOINT ["run-tests"]
CMD ["test-all", "cover"]
