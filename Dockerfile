# Base images are pinned by digest, not by tag alone: a tag is a moving
# reference, so `alpine:3.21` today and `alpine:3.21` next month are different
# builds of Cargo with the same source. The tag is kept in front of the digest
# so a reader can still see what it is. Dependabot bumps these
# (.github/dependabot.yml) — a digest pin without a bump mechanism just means
# the base image never gets its security updates.
FROM node:22-alpine@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32 AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -o /bin/cargod ./cmd/server

FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d
# docker-cli-buildx provides the BuildKit builder nixpacks requires (its
# generated Dockerfiles use cache mounts); without it nixpacks builds fail and
# plain Dockerfile builds fall back to the deprecated legacy builder.
RUN apk add --no-cache ca-certificates docker-cli docker-cli-buildx docker-cli-compose git curl
ARG NIXPACKS_VERSION=1.29.1
# The release is downloaded over TLS from GitHub, which authenticates the
# host but says nothing about the asset: a release can be re-uploaded, and a
# compromised one would land in the image unnoticed. The tarball is verified
# against this hash instead of being piped straight into tar. Bump both
# together — the build fails loudly if they disagree.
ARG NIXPACKS_SHA256=7ac8866e332486f4c717e4c7319e8fa4f3bffa949fb4a002a515a08703846525
RUN set -eu; \
    url="https://github.com/railwayapp/nixpacks/releases/download/v${NIXPACKS_VERSION}/nixpacks-v${NIXPACKS_VERSION}-x86_64-unknown-linux-musl.tar.gz"; \
    curl -fsSL -o /tmp/nixpacks.tar.gz "$url"; \
    echo "${NIXPACKS_SHA256}  /tmp/nixpacks.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/nixpacks.tar.gz -C /usr/local/bin nixpacks; \
    rm /tmp/nixpacks.tar.gz
COPY --from=build /bin/cargod /usr/local/bin/cargod
ENTRYPOINT ["cargod"]
