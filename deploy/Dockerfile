FROM node:24.22.3-alpine3.23 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.11-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/web/dist internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.23.4
RUN apk add --no-cache ca-certificates postgresql18-client \
    && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /server ./server
COPY LICENSE ./LICENSE
USER app
ENV APP_ENV=prod APP_LISTEN_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/livez || exit 1
ENTRYPOINT ["/app/server"]
