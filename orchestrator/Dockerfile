FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/orchestrator ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/orchestrator /usr/local/bin/orchestrator
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/orchestrator"]
