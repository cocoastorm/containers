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

COPY ./script.sh /app/script.sh
COPY ./entrypoint.sh /entrypoint.sh
CMD ["/entrypoint.sh"]
