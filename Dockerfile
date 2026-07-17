FROM node:22-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -o /bin/cargod ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates docker-cli docker-cli-compose
COPY --from=build /bin/cargod /usr/local/bin/cargod
ENTRYPOINT ["cargod"]
