FROM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
  go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/http-relay ./cmd/http-relay

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /out/http-relay /usr/local/bin/http-relay

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/http-relay"]
