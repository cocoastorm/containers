FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.26.5-alpine3.23@sha256:622e56dbc11a8cfe87cafa2331e9a201877271cbff918af53d3be315f3da88cc AS build
WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/qbittorrent-natpmp-sync ./cmd/qbittorrent-natpmp-sync

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build --chown=2000:2000 /out/qbittorrent-natpmp-sync /qbittorrent-natpmp-sync
USER 2000:2000
ENTRYPOINT ["/qbittorrent-natpmp-sync"]
