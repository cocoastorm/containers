target "docker-metadata-action" {}

variable "APP" { default = "qbittorrent-natpmp-sync" }
variable "VERSION" { default = "0.1.0" }
variable "SOURCE" { default = "https://github.com/cocoastorm/containers" }

group "default" { targets = ["image-local"] }

target "image" {
  inherits = ["docker-metadata-action"]
  args = { VERSION = "${VERSION}" }
  labels = { "org.opencontainers.image.source" = "${SOURCE}" }
}

target "image-local" {
  inherits = ["image"]
  output = ["type=docker"]
  tags = ["${APP}:${VERSION}"]
}

target "image-all" {
  inherits = ["image"]
  platforms = ["linux/amd64", "linux/arm64"]
}
