FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY internal/ internal/
COPY cmd/wechat-draft/main.go cmd/wechat-draft/main.go
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/wechat-draft ./cmd/wechat-draft

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 relay && \
    mkdir /state && chown relay:relay /state && chmod 0700 /state
COPY --from=build /out/wechat-draft /usr/local/bin/wechat-draft
USER relay
EXPOSE 8201
ENTRYPOINT ["wechat-draft", "--config", "/state/.env"]
