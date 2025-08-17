FROM docker.io/library/alpine:3.21

ARG TARGETPLATFORM
ARG TARGETARCH
ARG TARGETOS

ENV \
  TERM=xterm-256color

USER root
WORKDIR /app

RUN \
  apk add --no-cache \
  bash \
  ca-certificates \
  curl \
  gum \
  jq \
  supercronic

COPY --chmod=755 ./entrypoint.sh /entrypoint.sh

USER nobody:nogroup
WORKDIR /app
COPY --chown=65534:65534 --chmod=755 ./scripts/script.sh /app/script.sh

ENTRYPOINT ["/usr/bin/catatonit", "--", "/entrypoint.sh"]
