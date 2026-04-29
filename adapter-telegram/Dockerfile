FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/adapter-telegram ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/adapter-telegram /usr/local/bin/adapter-telegram
EXPOSE 8084
ENTRYPOINT ["/usr/local/bin/adapter-telegram"]
