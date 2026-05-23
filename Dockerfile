FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /densecloud-runtime ./go/examples/minimal

FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata
RUN addgroup -S densecloud && adduser -S -G densecloud -H -h /nonexistent densecloud

WORKDIR /app

COPY --from=builder /densecloud-runtime /app/densecloud-runtime
USER densecloud:densecloud

EXPOSE 8080

ENTRYPOINT ["/app/densecloud-runtime"]
