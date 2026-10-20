FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /leadfinder ./cmd/leadfinder

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app && mkdir /data && chown app /data
COPY --from=build /leadfinder /usr/local/bin/leadfinder
USER app
WORKDIR /data
ENV LF_DB=/data/leadfinder.db LF_ADDR=:8090
EXPOSE 8090
ENTRYPOINT ["leadfinder"]
CMD ["serve"]
