FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/tool-docker-creator ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/tool-docker-creator /usr/local/bin/tool-docker-creator
EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/tool-docker-creator"]
