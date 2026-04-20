FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/env-golang ./cmd/server

# Runtime must have the Go toolchain so /run can `go run` user code.
FROM golang:1.22-alpine
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/env-golang /usr/local/bin/env-golang
EXPOSE 8083
ENTRYPOINT ["/usr/local/bin/env-golang"]
