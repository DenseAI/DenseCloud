FROM golang:1.25.13-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
RUN module_cache="$(go env GOMODCACHE)" && \
    mkdir -p /third-party-licenses && \
    find "${module_cache}" -type f \
      \( -name 'LICENSE*' -o -name 'NOTICE*' -o -name 'COPYING*' \) \
      -exec sh -c 'cache_root="$1"; shift; for source_path do relative_path="${source_path#"${cache_root}"/}"; destination="/third-party-licenses/$(dirname "${relative_path}")"; mkdir -p "${destination}"; cp "${source_path}" "${destination}/"; done' sh "${module_cache}" {} +

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /densecloud-runtime ./go/examples/minimal

FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata
RUN addgroup -S densecloud && adduser -S -G densecloud -H -h /nonexistent densecloud

WORKDIR /app

COPY --from=builder /densecloud-runtime /app/densecloud-runtime
COPY --from=builder /app/LICENSE /usr/share/licenses/densecloud/LICENSE
COPY --from=builder /app/NOTICE /usr/share/licenses/densecloud/NOTICE
COPY --from=builder /app/THIRD_PARTY_NOTICES.md /usr/share/licenses/densecloud/THIRD_PARTY_NOTICES.md
COPY --from=builder /third-party-licenses /usr/share/licenses/densecloud/third-party
USER densecloud:densecloud

EXPOSE 8080

ENTRYPOINT ["/app/densecloud-runtime"]
