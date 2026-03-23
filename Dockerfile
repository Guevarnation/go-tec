FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETARCH=arm64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /bot ./cmd/bot

FROM gcr.io/distroless/static-debian12
COPY --from=builder /bot /bot
VOLUME /data
ENV DATA_DIR=/data
ENTRYPOINT ["/bot"]
