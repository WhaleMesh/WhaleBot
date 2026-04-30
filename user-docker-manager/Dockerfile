FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/user-docker-manager ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/user-docker-manager /usr/local/bin/user-docker-manager
EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/user-docker-manager"]
